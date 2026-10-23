use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;
use crate::Synth::Voice::Voice;
use crate::Synth::OscillatorConfig::OscillatorConfig;
use crate::Synth::FilterConfig::FilterConfig;

pub trait SynthEngine {
    fn render_block(
        &self,
        voice: &mut Voice,
        osc_config: &OscillatorConfig,
        filter_config: &FilterConfig,
        output: &mut [AudioFrame],
    );

    fn note_on(
        &self,
        voice: &mut Voice,
        note_id: NoteId,
        note_number: NoteNumber,
        velocity: Velocity,
    );

    fn note_off(&self, voice: &mut Voice, note_id: NoteId);

    fn is_finished(&self, voice: &Voice) -> bool;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Synth::EnvelopeStage::EnvelopeStage;

    struct StubSynthEngine;

    impl SynthEngine for StubSynthEngine {
        fn render_block(
            &self,
            _voice: &mut Voice,
            _osc_config: &OscillatorConfig,
            _filter_config: &FilterConfig,
            output: &mut [AudioFrame],
        ) {
            for frame in output.iter_mut() {
                *frame = AudioFrame::mono(0.5);
            }
        }

        fn note_on(
            &self,
            voice: &mut Voice,
            note_id: NoteId,
            note_number: NoteNumber,
            velocity: Velocity,
        ) {
            voice.note_on(note_id, note_number, velocity);
        }

        fn note_off(&self, voice: &mut Voice, note_id: NoteId) {
            voice.note_off(note_id);
        }

        fn is_finished(&self, voice: &Voice) -> bool {
            voice.envelope_stage() == EnvelopeStage::Idle && !voice.is_active()
        }
    }

    #[test]
    fn stub_renders_output() {
        let engine = StubSynthEngine;
        let mut voice = Voice::new();
        voice.note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(1.0).unwrap());

        let osc = OscillatorConfig::default();
        let filter = FilterConfig::default();
        let mut output = [AudioFrame::silence(); 64];
        engine.render_block(&mut voice, &osc, &filter, &mut output);

        assert!((output[0].left - 0.5).abs() < f32::EPSILON);
    }

    #[test]
    fn stub_note_on_activates_voice() {
        let engine = StubSynthEngine;
        let mut voice = Voice::new();
        engine.note_on(&mut voice, NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(1.0).unwrap());
        assert!(voice.is_active());
    }

    #[test]
    fn stub_note_off_releases_voice() {
        let engine = StubSynthEngine;
        let mut voice = Voice::new();
        engine.note_on(&mut voice, NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(1.0).unwrap());
        engine.note_off(&mut voice, NoteId::new(1));
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Release);
    }

    #[test]
    fn stub_is_finished_when_idle() {
        let engine = StubSynthEngine;
        let voice = Voice::new();
        assert!(engine.is_finished(&voice));
    }
}
