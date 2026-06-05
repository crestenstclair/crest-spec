use std::collections::HashMap;
use std::sync::mpsc::{self, Receiver, SyncSender};
use std::sync::{
    atomic::{AtomicU64, Ordering},
    Mutex,
};

use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};

use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::SampleRate::SampleRate;
use crate::Shell::AudioOutput::{AudioOutput, AudioStream};

const CHANNEL_BUFFER_FRAMES: usize = 4096;

struct ActiveStream {
    sender: SyncSender<AudioFrame>,
    _cpal_stream: cpal::Stream,
}

pub struct CpalAudioOutput {
    host: Box<dyn CpalHost>,
    streams: Mutex<HashMap<u64, ActiveStream>>,
    next_id: AtomicU64,
}

/// Abstraction over cpal host operations for testability.
/// Production code uses `DefaultCpalHost`; tests inject a mock.
pub trait CpalHost: Send + Sync {
    fn build_output_stream(
        &self,
        sample_rate: u32,
        receiver: Receiver<AudioFrame>,
    ) -> Result<cpal::Stream, CpalAudioError>;
}

#[derive(Debug)]
pub enum CpalAudioError {
    NoOutputDevice,
    NoSupportedConfig,
    BuildStreamFailed(String),
    PlayStreamFailed(String),
}

impl std::fmt::Display for CpalAudioError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NoOutputDevice => write!(f, "no default output device available"),
            Self::NoSupportedConfig => write!(f, "no supported output config found"),
            Self::BuildStreamFailed(e) => write!(f, "failed to build stream: {e}"),
            Self::PlayStreamFailed(e) => write!(f, "failed to play stream: {e}"),
        }
    }
}

impl std::error::Error for CpalAudioError {}

/// Production cpal host that talks to real audio hardware.
pub struct DefaultCpalHost;

impl CpalHost for DefaultCpalHost {
    fn build_output_stream(
        &self,
        sample_rate: u32,
        receiver: Receiver<AudioFrame>,
    ) -> Result<cpal::Stream, CpalAudioError> {
        let host = cpal::default_host();
        let device = host
            .default_output_device()
            .ok_or(CpalAudioError::NoOutputDevice)?;

        let config = cpal::StreamConfig {
            channels: 2,
            sample_rate: cpal::SampleRate(sample_rate),
            buffer_size: cpal::BufferSize::Default,
        };

        let stream = device
            .build_output_stream(
                &config,
                move |data: &mut [f32], _: &cpal::OutputCallbackInfo| {
                    write_interleaved_from_receiver(&receiver, data);
                },
                |err| {
                    eprintln!("cpal stream error: {err}");
                },
                None,
            )
            .map_err(|e| CpalAudioError::BuildStreamFailed(e.to_string()))?;

        stream
            .play()
            .map_err(|e| CpalAudioError::PlayStreamFailed(e.to_string()))?;

        Ok(stream)
    }
}

/// Drains frames from the receiver and writes interleaved stereo samples.
/// If the receiver is empty or disconnected, fills remaining output with silence.
fn write_interleaved_from_receiver(receiver: &Receiver<AudioFrame>, data: &mut [f32]) {
    let mut i = 0;
    while i + 1 < data.len() {
        match receiver.try_recv() {
            Ok(frame) => {
                data[i] = frame.left;
                data[i + 1] = frame.right;
            }
            Err(_) => {
                data[i] = 0.0;
                data[i + 1] = 0.0;
            }
        }
        i += 2;
    }
}

impl CpalAudioOutput {
    /// Creates a new CpalAudioOutput with the given host abstraction.
    pub fn new(host: Box<dyn CpalHost>) -> Self {
        Self {
            host,
            streams: Mutex::new(HashMap::new()),
            next_id: AtomicU64::new(1),
        }
    }

    /// Convenience constructor using the default cpal host.
    pub fn with_default_host() -> Self {
        Self::new(Box::new(DefaultCpalHost))
    }

    /// Closes and removes an active stream by id.
    pub fn close_stream(&self, stream: &AudioStream) {
        let mut streams = self.streams.lock().expect("streams lock poisoned");
        // Dropping the ActiveStream drops the cpal::Stream, which stops playback,
        // and drops the SyncSender, which signals the receiver to disconnect.
        streams.remove(&stream.id);
    }
}

impl AudioOutput for CpalAudioOutput {
    fn open_stream(&self, sample_rate: SampleRate) -> AudioStream {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        let (sender, receiver) = mpsc::sync_channel::<AudioFrame>(CHANNEL_BUFFER_FRAMES);

        let cpal_stream = self
            .host
            .build_output_stream(sample_rate.value(), receiver)
            .expect("failed to open audio output stream");

        let active = ActiveStream {
            sender,
            _cpal_stream: cpal_stream,
        };

        {
            let mut streams = self.streams.lock().expect("streams lock poisoned");
            streams.insert(id, active);
        }

        AudioStream { id }
    }

    fn write_buffer(&self, stream: &AudioStream, frames: &[AudioFrame]) {
        let streams = self.streams.lock().expect("streams lock poisoned");
        if let Some(active) = streams.get(&stream.id) {
            for &frame in frames {
                let _ = active.sender.send(frame);
            }
        }
    }
}

impl Drop for CpalAudioOutput {
    fn drop(&mut self) {
        // Drain all streams so cpal streams are stopped cleanly.
        let mut streams = self.streams.lock().expect("streams lock poisoned");
        streams.clear();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Arc, Mutex as StdMutex};

    /// Shared state for the mock host, accessible both from the mock and from tests.
    struct MockState {
        drain_handles: StdMutex<Vec<std::thread::JoinHandle<Vec<AudioFrame>>>>,
        collected: StdMutex<Vec<Vec<AudioFrame>>>,
    }

    impl MockState {
        fn new() -> Arc<Self> {
            Arc::new(Self {
                drain_handles: StdMutex::new(Vec::new()),
                collected: StdMutex::new(Vec::new()),
            })
        }

        /// Join all mock drain threads and collect their frames.
        fn flush(&self) {
            let mut handles = self.drain_handles.lock().unwrap();
            let mut collected = self.collected.lock().unwrap();
            for handle in handles.drain(..) {
                if let Ok(frames) = handle.join() {
                    collected.push(frames);
                }
            }
        }

        fn collected_frames(&self) -> Vec<Vec<AudioFrame>> {
            self.collected.lock().unwrap().clone()
        }
    }

    /// A mock CpalHost that spawns a background thread to drain each receiver
    /// (simulating the audio callback) and builds a silent cpal::Stream for
    /// type satisfaction.
    struct MockCpalHost {
        state: Arc<MockState>,
    }

    impl MockCpalHost {
        fn new(state: Arc<MockState>) -> Self {
            Self { state }
        }
    }

    impl CpalHost for MockCpalHost {
        fn build_output_stream(
            &self,
            _sample_rate: u32,
            receiver: Receiver<AudioFrame>,
        ) -> Result<cpal::Stream, CpalAudioError> {
            // Spawn a thread that drains the receiver, simulating the audio callback.
            let handle = std::thread::spawn(move || {
                let mut frames = Vec::new();
                while let Ok(frame) = receiver.recv() {
                    frames.push(frame);
                }
                frames
            });

            self.state.drain_handles.lock().unwrap().push(handle);

            // Build a real silent cpal::Stream to satisfy the type system.
            let host = cpal::default_host();
            if let Some(device) = host.default_output_device() {
                let config = cpal::StreamConfig {
                    channels: 2,
                    sample_rate: cpal::SampleRate(44100),
                    buffer_size: cpal::BufferSize::Default,
                };
                let stream = device
                    .build_output_stream(
                        &config,
                        |data: &mut [f32], _| {
                            for sample in data.iter_mut() {
                                *sample = 0.0;
                            }
                        },
                        |_| {},
                        None,
                    )
                    .map_err(|e| CpalAudioError::BuildStreamFailed(e.to_string()))?;
                Ok(stream)
            } else {
                Err(CpalAudioError::NoOutputDevice)
            }
        }
    }

    #[test]
    fn write_interleaved_fills_silence_when_empty() {
        let (_sender, receiver) = mpsc::sync_channel::<AudioFrame>(16);
        // Drop sender so receiver sees disconnection.
        drop(_sender);

        let mut buffer = [1.0_f32; 8];
        write_interleaved_from_receiver(&receiver, &mut buffer);

        assert_eq!(buffer, [0.0; 8]);
    }

    #[test]
    fn write_interleaved_writes_frames_then_silence() {
        let (sender, receiver) = mpsc::sync_channel::<AudioFrame>(16);
        sender.send(AudioFrame::new(0.5, -0.5)).unwrap();
        sender.send(AudioFrame::new(1.0, -1.0)).unwrap();
        drop(sender);

        let mut buffer = [0.0_f32; 8]; // 4 stereo frames worth of space
        write_interleaved_from_receiver(&receiver, &mut buffer);

        // First two frames written, rest filled with silence.
        assert_eq!(buffer[0], 0.5);
        assert_eq!(buffer[1], -0.5);
        assert_eq!(buffer[2], 1.0);
        assert_eq!(buffer[3], -1.0);
        assert_eq!(buffer[4], 0.0);
        assert_eq!(buffer[5], 0.0);
        assert_eq!(buffer[6], 0.0);
        assert_eq!(buffer[7], 0.0);
    }

    #[test]
    fn stream_ids_are_unique_and_sequential() {
        let state = MockState::new();
        let output = CpalAudioOutput::new(Box::new(MockCpalHost::new(Arc::clone(&state))));
        let sr = SampleRate::new(44100).unwrap();

        let s1 = output.open_stream(sr);
        let s2 = output.open_stream(sr);

        assert_ne!(s1.id, s2.id);
        assert_eq!(s1.id + 1, s2.id);

        output.close_stream(&s1);
        output.close_stream(&s2);
    }

    #[test]
    fn write_buffer_sends_frames_through_channel() {
        let state = MockState::new();
        let output = CpalAudioOutput::new(Box::new(MockCpalHost::new(Arc::clone(&state))));
        let sr = SampleRate::new(48000).unwrap();

        let stream = output.open_stream(sr);

        let frames = [
            AudioFrame::new(0.1, 0.2),
            AudioFrame::new(0.3, 0.4),
            AudioFrame::new(0.5, 0.6),
        ];
        output.write_buffer(&stream, &frames);

        // Close the stream to drop the sender, which lets the mock drain thread finish.
        output.close_stream(&stream);

        // Flush joins the drain threads and collects their frames.
        state.flush();

        let all = state.collected_frames();
        assert_eq!(all.len(), 1);
        assert_eq!(all[0].len(), 3);
        assert_eq!(all[0][0], AudioFrame::new(0.1, 0.2));
        assert_eq!(all[0][1], AudioFrame::new(0.3, 0.4));
        assert_eq!(all[0][2], AudioFrame::new(0.5, 0.6));
    }

    #[test]
    fn close_stream_removes_active_stream() {
        let state = MockState::new();
        let output = CpalAudioOutput::new(Box::new(MockCpalHost::new(Arc::clone(&state))));
        let sr = SampleRate::new(44100).unwrap();

        let stream = output.open_stream(sr);
        output.close_stream(&stream);

        // Writing to a closed stream should be a no-op (no panic).
        let frames = [AudioFrame::new(1.0, 1.0)];
        output.write_buffer(&stream, &frames);
    }

    #[test]
    fn drop_clears_all_streams() {
        let state = MockState::new();
        let output = CpalAudioOutput::new(Box::new(MockCpalHost::new(Arc::clone(&state))));
        let sr = SampleRate::new(44100).unwrap();

        let _s1 = output.open_stream(sr);
        let _s2 = output.open_stream(sr);

        drop(output);
        // No panic, streams cleaned up.
    }
}
