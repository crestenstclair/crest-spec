use crest_synth::Synth::AudioRenderer::AudioRenderer;
use crest_synth::Synth::SynthEngine::SynthEngine;
use crest_synth::Synth::Voice::{Voice, NoteOn, NoteOff};
use crest_synth::Synth::OscillatorConfig::OscillatorConfig;
use crest_synth::Synth::FilterConfig::FilterConfig;
use crest_synth::kernel::AudioFrame::AudioFrame;
use crest_synth::kernel::NoteId::NoteId;
use crest_synth::kernel::NoteNumber::NoteNumber;
use crest_synth::kernel::Velocity::Velocity;

struct MockSynthEngine {
    fill_value: f32,
}

impl MockSynthEngine {
    fn new(fill_value: f32) -> Self {
        Self { fill_value }
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
            frame.left = self.fill_value;
            frame.right = self.fill_value;
        }
    }

    fn note_on(&self, voice: Voice, _cmd: &NoteOn) -> Voice {
        voice
    }

    fn note_off(&self, voice: Voice, _cmd: &NoteOff) -> Voice {
        voice
    }

    fn is_finished(&self, _voice: &Voice) -> bool {
        false
    }
}

fn make_active_voice(id: u32) -> Voice {
    let mut voice = Voice::new();
    let cmd = NoteOn {
        note_id: NoteId::new(id),
        note_number: NoteNumber::new(60).unwrap(),
        velocity: Velocity::new(0.8).unwrap(),
    };
    voice.note_on(cmd);
    voice
}

#[test]
fn render_clears_output_when_no_voices() {
    let engine = MockSynthEngine::new(1.0);
    let renderer = AudioRenderer::new(engine);
    let mut output = [AudioFrame { left: 99.0, right: 99.0 }; 4];

    renderer.render(&[], &OscillatorConfig::default(), &FilterConfig::default(), &mut output);

    for frame in &output {
        assert_eq!(frame.left, 0.0);
        assert_eq!(frame.right, 0.0);
    }
}

#[test]
fn render_clears_output_when_no_active_voices() {
    let engine = MockSynthEngine::new(1.0);
    let renderer = AudioRenderer::new(engine);
    let idle_voice = Voice::default();
    let mut output = [AudioFrame { left: 99.0, right: 99.0 }; 4];

    renderer.render(
        &[idle_voice],
        &OscillatorConfig::default(),
        &FilterConfig::default(),
        &mut output,
    );

    for frame in &output {
        assert_eq!(frame.left, 0.0);
        assert_eq!(frame.right, 0.0);
    }
}

#[test]
fn render_single_active_voice() {
    let engine = MockSynthEngine::new(0.5);
    let renderer = AudioRenderer::new(engine);
    let voice = make_active_voice(1);
    let mut output = [AudioFrame::default(); 8];

    renderer.render(
        &[voice],
        &OscillatorConfig::default(),
        &FilterConfig::default(),
        &mut output,
    );

    for frame in &output {
        assert_eq!(frame.left, 0.5);
        assert_eq!(frame.right, 0.5);
    }
}

#[test]
fn render_mixes_multiple_active_voices() {
    let engine = MockSynthEngine::new(0.25);
    let renderer = AudioRenderer::new(engine);
    let voices = [make_active_voice(1), make_active_voice(2), make_active_voice(3)];
    let mut output = [AudioFrame::default(); 4];

    renderer.render(
        &voices,
        &OscillatorConfig::default(),
        &FilterConfig::default(),
        &mut output,
    );

    for frame in &output {
        assert!((frame.left - 0.75).abs() < f32::EPSILON);
        assert!((frame.right - 0.75).abs() < f32::EPSILON);
    }
}

#[test]
fn render_skips_inactive_voices_in_mix() {
    let engine = MockSynthEngine::new(1.0);
    let renderer = AudioRenderer::new(engine);
    let voices = [make_active_voice(1), Voice::default(), make_active_voice(2)];
    let mut output = [AudioFrame::default(); 4];

    renderer.render(
        &voices,
        &OscillatorConfig::default(),
        &FilterConfig::default(),
        &mut output,
    );

    for frame in &output {
        assert_eq!(frame.left, 2.0);
        assert_eq!(frame.right, 2.0);
    }
}

#[test]
#[should_panic(expected = "output length exceeds MAX_BLOCK_SIZE")]
fn render_panics_if_output_exceeds_max_block_size() {
    let engine = MockSynthEngine::new(1.0);
    let renderer = AudioRenderer::new(engine);
    let mut output = [AudioFrame::default(); 513];

    renderer.render(
        &[],
        &OscillatorConfig::default(),
        &FilterConfig::default(),
        &mut output,
    );
}
