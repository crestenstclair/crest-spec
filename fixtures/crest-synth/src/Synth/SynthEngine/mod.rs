use crate::kernel::AudioFrame::AudioFrame;
use crate::Synth::FilterConfig::FilterConfig;
use crate::Synth::OscillatorConfig::OscillatorConfig;
use crate::Synth::Voice::{NoteOff, NoteOn, Voice};

pub trait SynthEngine {
    fn render_block(
        &self,
        voice: &Voice,
        osc_config: &OscillatorConfig,
        filter_config: &FilterConfig,
        output: &mut [AudioFrame],
    );

    fn note_on(&self, voice: Voice, cmd: &NoteOn) -> Voice;

    fn note_off(&self, voice: Voice, cmd: &NoteOff) -> Voice;

    fn is_finished(&self, voice: &Voice) -> bool;
}
