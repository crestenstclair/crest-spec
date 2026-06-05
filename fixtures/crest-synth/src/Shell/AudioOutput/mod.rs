use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::SampleRate::SampleRate;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct AudioStream {
    pub id: u64,
}

pub trait AudioOutput {
    fn open_stream(&self, sample_rate: SampleRate) -> AudioStream;
    fn write_buffer(&self, stream: &AudioStream, frames: &[AudioFrame]);
}
