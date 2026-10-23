// path: src/main.rs

use std::io::Write;

use crest_synth::kernel::AudioFrame::AudioFrame;
use crest_synth::kernel::NoteId::NoteId;
use crest_synth::kernel::NoteNumber::NoteNumber;
use crest_synth::kernel::SampleRate::SampleRate;
use crest_synth::kernel::Velocity::Velocity;
use crest_synth::Shell::AudioOutput::AudioOutput;
use crest_synth::Shell::CpalAudioOutput::CpalAudioOutput;
use crest_synth::Synth::AudioRenderer::AudioRenderer;
use crest_synth::Synth::EnvelopeStage::EnvelopeStage;
use crest_synth::Synth::FilterConfig::FilterConfig;
use crest_synth::Synth::OscillatorConfig::OscillatorConfig;
use crest_synth::Synth::SynthEngine::SynthEngine;
use crest_synth::Synth::Voice::Voice;
use crest_synth::Synth::VoiceAllocator::VoiceAllocator;

// ---------------------------------------------------------------------------
// SimpleSineEngine — a concrete SynthEngine that renders sine waves.
//
// Per-voice phase is tracked via Voice::oscillator_phase / set_oscillator_phase,
// so render_block takes &self (no interior mutability needed).
// ---------------------------------------------------------------------------

struct SimpleSineEngine {
    sample_rate: SampleRate,
}

impl SimpleSineEngine {
    fn new(sample_rate: SampleRate) -> Self {
        Self { sample_rate }
    }
}

impl SynthEngine for SimpleSineEngine {
    fn render_block(
        &self,
        voice: &mut Voice,
        _osc_config: &OscillatorConfig,
        _filter_config: &FilterConfig,
        output: &mut [AudioFrame],
    ) {
        if !voice.is_active() {
            for frame in output.iter_mut() {
                *frame = AudioFrame::silence();
            }
            return;
        }

        let freq_hz = voice.frequency().value();
        let sr = self.sample_rate.value() as f64;
        let phase_inc = freq_hz / sr;
        let velocity = voice.velocity().value() as f32;
        let mut phase = voice.oscillator_phase();

        for frame in output.iter_mut() {
            let sample = (phase * 2.0 * std::f64::consts::PI).sin() as f32 * velocity;
            *frame = AudioFrame::mono(sample);
            phase += phase_inc;
            if phase >= 1.0 {
                phase -= 1.0;
            }
        }

        voice.set_oscillator_phase(phase);
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

// ---------------------------------------------------------------------------
// WavWriter — writes 16-bit stereo PCM WAV files using std::io only.
// ---------------------------------------------------------------------------

struct WavWriter {
    samples: Vec<AudioFrame>,
    sample_rate: u32,
}

impl WavWriter {
    fn new(sample_rate: u32) -> Self {
        Self {
            samples: Vec::new(),
            sample_rate,
        }
    }

    fn extend(&mut self, frames: &[AudioFrame]) {
        self.samples.extend_from_slice(frames);
    }

    fn write_to_file(&self, path: &str) -> std::io::Result<()> {
        let mut file = std::fs::File::create(path)?;
        let num_samples = self.samples.len() as u32;
        let num_channels: u16 = 2;
        let bits_per_sample: u16 = 16;
        let byte_rate = self.sample_rate * num_channels as u32 * bits_per_sample as u32 / 8;
        let block_align = num_channels * bits_per_sample / 8;
        let data_size = num_samples * num_channels as u32 * bits_per_sample as u32 / 8;

        // RIFF header
        file.write_all(b"RIFF")?;
        file.write_all(&(36 + data_size).to_le_bytes())?;
        file.write_all(b"WAVE")?;

        // fmt sub-chunk
        file.write_all(b"fmt ")?;
        file.write_all(&16u32.to_le_bytes())?;
        file.write_all(&1u16.to_le_bytes())?; // PCM
        file.write_all(&num_channels.to_le_bytes())?;
        file.write_all(&self.sample_rate.to_le_bytes())?;
        file.write_all(&byte_rate.to_le_bytes())?;
        file.write_all(&block_align.to_le_bytes())?;
        file.write_all(&bits_per_sample.to_le_bytes())?;

        // data sub-chunk
        file.write_all(b"data")?;
        file.write_all(&data_size.to_le_bytes())?;

        for frame in &self.samples {
            let left = (frame.left.clamp(-1.0, 1.0) * 32767.0) as i16;
            let right = (frame.right.clamp(-1.0, 1.0) * 32767.0) as i16;
            file.write_all(&left.to_le_bytes())?;
            file.write_all(&right.to_le_bytes())?;
        }

        Ok(())
    }
}

// ---------------------------------------------------------------------------
// Arpeggio schedule: C4 (60), E4 (64), G4 (67)
// ---------------------------------------------------------------------------

const NOTES: [(u8, f64, f64); 3] = [
    (60, 0.0, 0.4),  // C4 at t=0.0 for 0.4s
    (64, 0.5, 0.4),  // E4 at t=0.5 for 0.4s
    (67, 1.0, 0.4),  // G4 at t=1.0 for 0.4s
];

// ---------------------------------------------------------------------------
// WAV mode — offline render to tone-test.wav
// ---------------------------------------------------------------------------

fn run_wav_mode() {
    let sample_rate_hz = 44100u32;
    let sample_rate = SampleRate::new(sample_rate_hz).unwrap();
    let block_size = 256usize;
    let total_seconds = 3.0f64;
    let total_samples = (total_seconds * sample_rate_hz as f64) as usize;

    let engine = SimpleSineEngine::new(sample_rate);
    let osc_config = OscillatorConfig::default();
    let filter_config = FilterConfig::default();
    let renderer = AudioRenderer::new(engine, osc_config, filter_config);

    let mut allocator = VoiceAllocator::new(8);
    let mut wav = WavWriter::new(sample_rate_hz);
    let mut buffer = vec![AudioFrame::silence(); block_size];
    let mut sample_pos = 0usize;

    // Keep a separate engine ref for note_on/note_off via the renderer's engine accessor.
    while sample_pos < total_samples {
        let block_end = (sample_pos + block_size).min(total_samples);
        let this_block = block_end - sample_pos;

        trigger_notes_via_renderer(
            &mut allocator,
            &renderer,
            sample_pos,
            block_end,
            sample_rate_hz,
        );

        renderer.render(allocator.voices_mut(), &mut buffer[..this_block]);
        wav.extend(&buffer[..this_block]);
        sample_pos = block_end;
    }

    let output_path = "tone-test.wav";
    wav.write_to_file(output_path)
        .expect("failed to write WAV file");
    println!("Wrote {output_path}: 3-second C4-E4-G4 arpeggio at {sample_rate_hz} Hz stereo");
}

/// Trigger note events using the engine inside the AudioRenderer.
fn trigger_notes_via_renderer(
    allocator: &mut VoiceAllocator,
    renderer: &AudioRenderer<SimpleSineEngine>,
    sample_pos: usize,
    block_end: usize,
    sample_rate_hz: u32,
) {
    let velocity = Velocity::new(0.8).unwrap();
    for (i, &(note, start, duration)) in NOTES.iter().enumerate() {
        let start_sample = (start * sample_rate_hz as f64) as usize;
        let end_sample = ((start + duration) * sample_rate_hz as f64) as usize;

        if start_sample >= sample_pos && start_sample < block_end {
            let note_id = NoteId::new(i as u32);
            let note_number = NoteNumber::new(note).unwrap();
            let (idx, _event) = allocator.note_on(note_id, note_number, velocity);
            renderer
                .engine()
                .note_on(&mut allocator.voices_mut()[idx], note_id, note_number, velocity);
        }
        if end_sample >= sample_pos && end_sample < block_end {
            let note_id = NoteId::new(i as u32);
            if let Some((idx, _event)) = allocator.note_off(note_id) {
                renderer
                    .engine()
                    .note_off(&mut allocator.voices_mut()[idx], note_id);
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Real-time mode — play through speakers via CpalAudioOutput
// ---------------------------------------------------------------------------

fn run_realtime_mode() {
    let sample_rate_hz = 44100u32;
    let sample_rate = SampleRate::new(sample_rate_hz).unwrap();
    let block_size = 256usize;
    let total_seconds = 3.0f64;
    let total_samples = (total_seconds * sample_rate_hz as f64) as usize;

    println!("Playing C4-E4-G4 arpeggio through speakers...");

    let mut audio_output = CpalAudioOutput::new();
    let _stream = audio_output
        .open_stream(sample_rate)
        .expect("failed to open audio stream");

    let engine = SimpleSineEngine::new(sample_rate);
    let osc_config = OscillatorConfig::default();
    let filter_config = FilterConfig::default();
    let renderer = AudioRenderer::new(engine, osc_config, filter_config);

    let mut allocator = VoiceAllocator::new(8);
    let mut buffer = vec![AudioFrame::silence(); block_size];
    let mut sample_pos = 0usize;

    while sample_pos < total_samples {
        let block_end = (sample_pos + block_size).min(total_samples);
        let this_block = block_end - sample_pos;

        trigger_notes_via_renderer(
            &mut allocator,
            &renderer,
            sample_pos,
            block_end,
            sample_rate_hz,
        );

        renderer.render(allocator.voices_mut(), &mut buffer[..this_block]);
        audio_output
            .write_buffer(&buffer[..this_block])
            .expect("failed to write audio");
        sample_pos = block_end;
    }

    std::thread::sleep(std::time::Duration::from_secs(3));
}

// ---------------------------------------------------------------------------
// Entry point — dispatch on --wav flag
// ---------------------------------------------------------------------------

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.iter().any(|a| a == "--wav") {
        run_wav_mode();
    } else {
        run_realtime_mode();
    }
}
