use crest_synth::kernel::AudioFrame::AudioFrame;
use crest_synth::Synth::FilterConfig::FilterConfig;
use crest_synth::Synth::OscillatorConfig::OscillatorConfig;
use crest_synth::Synth::SynthEngine::SynthEngine;
use crest_synth::Synth::Voice::{NoteOff, NoteOn, Voice};

struct MockSynthEngine {
    render_value: f32,
    finished: bool,
}

impl MockSynthEngine {
    fn new(render_value: f32, finished: bool) -> Self {
        Self {
            render_value,
            finished,
        }
    }
}

impl SynthEngine for MockSynthEngine {
    fn render_block(
        &self,
        _voice: &Voice,
        _osc_config: &OscillatorConfig,
        _filter_config: &FilterConfig,
        output: &mut [AudioFrame],
    ) {
        for frame in output.iter_mut() {
            frame.left = self.render_value;
            frame.right = self.render_value;
        }
    }

    fn note_on(&self, voice: Voice, _cmd: &NoteOn) -> Voice {
        voice
    }

    fn note_off(&self, voice: Voice, _cmd: &NoteOff) -> Voice {
        voice
    }

    fn is_finished(&self, _voice: &Voice) -> bool {
        self.finished
    }
}

fn render_with_engine<E: SynthEngine>(
    engine: &E,
    voice: &Voice,
    osc_config: &OscillatorConfig,
    filter_config: &FilterConfig,
    output: &mut [AudioFrame],
) {
    engine.render_block(voice, osc_config, filter_config, output);
}

#[test]
fn render_block_fills_output_buffer() {
    let engine = MockSynthEngine::new(0.5, false);
    let voice = Voice::default();
    let osc_config = OscillatorConfig::default();
    let filter_config = FilterConfig::default();
    let mut buffer = [AudioFrame { left: 0.0, right: 0.0 }; 64];

    render_with_engine(&engine, &voice, &osc_config, &filter_config, &mut buffer);

    for frame in &buffer {
        assert_eq!(frame.left, 0.5);
        assert_eq!(frame.right, 0.5);
    }
}

#[test]
fn render_block_handles_empty_buffer() {
    let engine = MockSynthEngine::new(1.0, false);
    let voice = Voice::default();
    let osc_config = OscillatorConfig::default();
    let filter_config = FilterConfig::default();
    let mut buffer: [AudioFrame; 0] = [];

    engine.render_block(&voice, &osc_config, &filter_config, &mut buffer);
}

#[test]
fn is_finished_returns_configured_value() {
    let voice = Voice::default();

    let active_engine = MockSynthEngine::new(0.0, false);
    assert!(!active_engine.is_finished(&voice));

    let finished_engine = MockSynthEngine::new(0.0, true);
    assert!(finished_engine.is_finished(&voice));
}

#[test]
fn trait_is_usable_with_generics() {
    fn process_voice<E: SynthEngine>(engine: &E, voice: Voice) -> bool {
        engine.is_finished(&voice)
    }

    let engine = MockSynthEngine::new(0.0, true);
    let voice = Voice::default();
    assert!(process_voice(&engine, voice));
}
