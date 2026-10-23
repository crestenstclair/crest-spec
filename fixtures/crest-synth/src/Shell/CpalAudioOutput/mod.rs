use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use std::sync::mpsc;

use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::SampleRate::SampleRate;
use crate::Shell::AudioOutput::AudioOutput;

#[derive(Debug)]
pub enum CpalAudioError {
    NoDevice,
    BuildStream(String),
    PlayStream(String),
    NoStream,
    SendFailed,
}

pub struct CpalAudioStream {
    stream: cpal::Stream,
    sample_rate: SampleRate,
}

impl CpalAudioStream {
    pub fn sample_rate(&self) -> SampleRate {
        self.sample_rate
    }
}

impl Drop for CpalAudioStream {
    fn drop(&mut self) {
        self.stream.pause().ok();
    }
}

pub struct CpalAudioOutput {
    sender: Option<mpsc::SyncSender<AudioFrame>>,
}

impl CpalAudioOutput {
    pub fn new() -> Self {
        Self { sender: None }
    }
}

impl Default for CpalAudioOutput {
    fn default() -> Self {
        Self::new()
    }
}

impl AudioOutput for CpalAudioOutput {
    type Stream = CpalAudioStream;
    type Error = CpalAudioError;

    fn open_stream(&mut self, sample_rate: SampleRate) -> Result<Self::Stream, Self::Error> {
        let host = cpal::default_host();
        let device = host
            .default_output_device()
            .ok_or(CpalAudioError::NoDevice)?;

        let config = cpal::StreamConfig {
            channels: 2,
            sample_rate: cpal::SampleRate(sample_rate.value()),
            buffer_size: cpal::BufferSize::Default,
        };

        let (sender, receiver) = mpsc::sync_channel::<AudioFrame>(4096);

        let stream = device
            .build_output_stream(
                &config,
                move |data: &mut [f32], _: &cpal::OutputCallbackInfo| {
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
                },
                |err| {
                    eprintln!("audio stream error: {}", err);
                },
                None,
            )
            .map_err(|e| CpalAudioError::BuildStream(e.to_string()))?;

        stream
            .play()
            .map_err(|e| CpalAudioError::PlayStream(e.to_string()))?;
        self.sender = Some(sender);

        Ok(CpalAudioStream {
            stream,
            sample_rate,
        })
    }

    fn write_buffer(&mut self, frames: &[AudioFrame]) -> Result<(), Self::Error> {
        let sender = self.sender.as_ref().ok_or(CpalAudioError::NoStream)?;
        for &frame in frames {
            sender.send(frame).map_err(|_| CpalAudioError::SendFailed)?;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn new_creates_instance() {
        let output = CpalAudioOutput::new();
        assert!(output.sender.is_none());
    }

    #[test]
    fn default_creates_instance() {
        let output = CpalAudioOutput::default();
        assert!(output.sender.is_none());
    }

    #[test]
    fn write_buffer_without_stream_returns_error() {
        let mut output = CpalAudioOutput::new();
        let frames = [AudioFrame::silence()];
        let result = output.write_buffer(&frames);
        assert!(result.is_err());
    }

    #[test]
    fn error_debug_format() {
        let err = CpalAudioError::NoDevice;
        let _debug = format!("{:?}", err);
    }
}
