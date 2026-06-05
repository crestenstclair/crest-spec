use crate::Synth::SynthEngine::SynthEngine;
use crate::Synth::Voice::Voice;
use crate::Synth::OscillatorConfig::OscillatorConfig;
use crate::Synth::FilterConfig::FilterConfig;
use crate::kernel::AudioFrame::AudioFrame;

const MAX_BLOCK_SIZE: usize = 512;

pub struct AudioRenderer<E: SynthEngine> {
    engine: E,
}

impl<E: SynthEngine> AudioRenderer<E> {
    pub fn new(engine: E) -> Self {
        Self { engine }
    }

    pub fn render(
        &self,
        voices: &[Voice],
        osc_config: &OscillatorConfig,
        filter_config: &FilterConfig,
        output: &mut [AudioFrame],
    ) {
        let len = output.len();
        assert!(len <= MAX_BLOCK_SIZE, "output length exceeds MAX_BLOCK_SIZE");

        for frame in output.iter_mut() {
            *frame = AudioFrame::default();
        }

        let mut scratch = [AudioFrame::default(); MAX_BLOCK_SIZE];

        for voice in voices {
            if !voice.is_active() {
                continue;
            }

            for frame in scratch[..len].iter_mut() {
                *frame = AudioFrame::default();
            }

            self.engine.render_block(voice, osc_config, filter_config, &mut scratch[..len]);

            for (out, s) in output.iter_mut().zip(scratch[..len].iter()) {
                *out += *s;
            }
        }
    }
}
