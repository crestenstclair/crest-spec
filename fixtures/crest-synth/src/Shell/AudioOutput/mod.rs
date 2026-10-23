use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::SampleRate::SampleRate;

pub trait AudioOutput {
    type Stream;
    type Error: std::fmt::Debug;

    fn open_stream(&mut self, sample_rate: SampleRate) -> Result<Self::Stream, Self::Error>;
    fn write_buffer(&mut self, frames: &[AudioFrame]) -> Result<(), Self::Error>;
}

#[cfg(test)]
mod tests {
    use super::*;

    struct MockAudioOutput {
        opened: bool,
        written_frames: Vec<AudioFrame>,
    }

    impl MockAudioOutput {
        fn new() -> Self {
            Self {
                opened: false,
                written_frames: Vec::new(),
            }
        }
    }

    impl AudioOutput for MockAudioOutput {
        type Stream = ();
        type Error = &'static str;

        fn open_stream(&mut self, _sample_rate: SampleRate) -> Result<Self::Stream, Self::Error> {
            self.opened = true;
            Ok(())
        }

        fn write_buffer(&mut self, frames: &[AudioFrame]) -> Result<(), Self::Error> {
            self.written_frames.extend_from_slice(frames);
            Ok(())
        }
    }

    #[test]
    fn mock_implements_trait() {
        let mut output = MockAudioOutput::new();
        let sr = SampleRate::new(44100).unwrap();
        output.open_stream(sr).unwrap();
        assert!(output.opened);
    }

    #[test]
    fn write_buffer() {
        let mut output = MockAudioOutput::new();
        let frames = [AudioFrame::mono(0.5), AudioFrame::mono(0.3)];
        output.write_buffer(&frames).unwrap();
        assert_eq!(output.written_frames.len(), 2);
    }
}
