use crate::kernel::AudioFrame::AudioFrame;
use crate::Synth::Voice::Voice;
use crate::Synth::OscillatorConfig::OscillatorConfig;
use crate::Synth::FilterConfig::FilterConfig;
use crate::Synth::SynthEngine::SynthEngine;

pub struct AudioRenderer<E: SynthEngine> {
    engine: E,
    osc_config: OscillatorConfig,
    filter_config: FilterConfig,
}

impl<E: SynthEngine> AudioRenderer<E> {
    pub fn new(engine: E, osc_config: OscillatorConfig, filter_config: FilterConfig) -> Self {
        Self {
            engine,
            osc_config,
            filter_config,
        }
    }

    pub fn render(&self, voices: &mut [Voice], output: &mut [AudioFrame]) {
        for frame in output.iter_mut() {
            *frame = AudioFrame::silence();
        }

        let block_size = output.len();
        if block_size == 0 {
            return;
        }

        let mut voice_buf = vec![AudioFrame::silence(); block_size];

        for voice in voices.iter_mut() {
            if !voice.is_active() {
                continue;
            }

            for frame in voice_buf.iter_mut() {
                *frame = AudioFrame::silence();
            }

            self.engine.render_block(voice, &self.osc_config, &self.filter_config, &mut voice_buf);

            for (out, rendered) in output.iter_mut().zip(voice_buf.iter()) {
                *out += *rendered;
            }

            if self.engine.is_finished(voice) {
                voice.finish();
            }
        }
    }

    pub fn engine(&self) -> &E {
        &self.engine
    }

    pub fn set_osc_config(&mut self, config: OscillatorConfig) {
        self.osc_config = config;
    }

    pub fn set_filter_config(&mut self, config: FilterConfig) {
        self.filter_config = config;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::kernel::NoteId::NoteId;
    use crate::kernel::NoteNumber::NoteNumber;
    use crate::kernel::Velocity::Velocity;
    use crate::Synth::EnvelopeStage::EnvelopeStage;

    struct TestEngine {
        sample_value: f32,
    }

    impl TestEngine {
        fn new(sample_value: f32) -> Self {
            Self { sample_value }
        }
    }

    impl SynthEngine for TestEngine {
        fn render_block(
            &self,
            _voice: &mut Voice,
            _osc_config: &OscillatorConfig,
            _filter_config: &FilterConfig,
            output: &mut [AudioFrame],
        ) {
            for frame in output.iter_mut() {
                *frame = AudioFrame::mono(self.sample_value);
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
    fn renders_silence_with_no_active_voices() {
        let renderer = AudioRenderer::new(
            TestEngine::new(0.5),
            OscillatorConfig::default(),
            FilterConfig::default(),
        );
        let mut voices = [Voice::new(), Voice::new()];
        let mut output = [AudioFrame::silence(); 4];
        renderer.render(&mut voices, &mut output);

        for frame in &output {
            assert_eq!(frame.left, 0.0);
            assert_eq!(frame.right, 0.0);
        }
    }

    #[test]
    fn renders_single_active_voice() {
        let renderer = AudioRenderer::new(
            TestEngine::new(0.25),
            OscillatorConfig::default(),
            FilterConfig::default(),
        );
        let mut voices = [Voice::new()];
        voices[0].note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(1.0).unwrap());

        let mut output = [AudioFrame::silence(); 4];
        renderer.render(&mut voices, &mut output);

        for frame in &output {
            assert!((frame.left - 0.25).abs() < f32::EPSILON);
        }
    }

    #[test]
    fn mixes_multiple_voices() {
        let renderer = AudioRenderer::new(
            TestEngine::new(0.3),
            OscillatorConfig::default(),
            FilterConfig::default(),
        );
        let mut voices = [Voice::new(), Voice::new()];
        voices[0].note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(1.0).unwrap());
        voices[1].note_on(NoteId::new(2), NoteNumber::new(64).unwrap(), Velocity::new(1.0).unwrap());

        let mut output = [AudioFrame::silence(); 4];
        renderer.render(&mut voices, &mut output);

        for frame in &output {
            assert!((frame.left - 0.6).abs() < 0.001);
        }
    }

    #[test]
    fn empty_output_buffer() {
        let renderer = AudioRenderer::new(
            TestEngine::new(0.5),
            OscillatorConfig::default(),
            FilterConfig::default(),
        );
        let mut voices = [Voice::new()];
        let mut output: [AudioFrame; 0] = [];
        renderer.render(&mut voices, &mut output);
    }

    #[test]
    fn config_setters() {
        let mut renderer = AudioRenderer::new(
            TestEngine::new(0.5),
            OscillatorConfig::default(),
            FilterConfig::default(),
        );

        use crate::Synth::OscillatorConfig::Waveform;
        let new_osc = OscillatorConfig::new(Waveform::Square, 5.0, 0.3);
        renderer.set_osc_config(new_osc);

        use crate::kernel::Frequency::Frequency;
        use crate::Synth::FilterConfig::FilterType;
        let new_filter = FilterConfig::new(
            Frequency::new(2000.0).unwrap(),
            0.5,
            FilterType::HighPass,
        ).unwrap();
        renderer.set_filter_config(new_filter);
    }
}
