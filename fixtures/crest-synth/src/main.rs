use crest_synth::Audio::AudioRenderer::AudioRenderer;
use crest_synth::Shell::AudioOutput::{AudioOutput, AudioStream};
use crest_synth::Shell::CpalAudioOutput::CpalAudioOutput;
use crest_synth::kernel::AudioFrame::AudioFrame;
use crest_synth::kernel::NoteId::NoteId;
use crest_synth::kernel::NoteNumber::NoteNumber;
use crest_synth::kernel::SampleRate::SampleRate;
use crest_synth::kernel::Velocity::Velocity;

use std::io::Write;

const SAMPLE_RATE: u32 = 44100;
const BLOCK_SIZE: usize = 256;

struct NoteEvent {
    sample: usize,
    note_id: NoteId,
    note_number: NoteNumber,
    velocity: Velocity,
    is_on: bool,
}

fn build_arpeggio_events(sample_rate: u32) -> Vec<NoteEvent> {
    let notes: [(u32, u8, f64, f64); 3] = [
        (1, 60, 0.0, 0.4),
        (2, 64, 0.5, 0.4),
        (3, 67, 1.0, 0.4),
    ];

    let mut events = Vec::new();
    for (id, midi, onset_sec, dur_sec) in &notes {
        let on_sample = (*onset_sec * sample_rate as f64) as usize;
        let off_sample = ((*onset_sec + *dur_sec) * sample_rate as f64) as usize;

        events.push(NoteEvent {
            sample: on_sample,
            note_id: NoteId::new(*id),
            note_number: NoteNumber::new(*midi).expect("valid note number"),
            velocity: Velocity::new(0.8).expect("valid velocity"),
            is_on: true,
        });
        events.push(NoteEvent {
            sample: off_sample,
            note_id: NoteId::new(*id),
            note_number: NoteNumber::new(*midi).expect("valid note number"),
            velocity: Velocity::new(0.0).expect("valid velocity"),
            is_on: false,
        });
    }

    events.sort_by_key(|e| e.sample);
    events
}

fn run_realtime() {
    println!("Playing C4-E4-G4 arpeggio through speakers...");

    let rate = SampleRate::new(SAMPLE_RATE).expect("valid sample rate");
    let output = CpalAudioOutput::with_default_host();
    let stream: AudioStream = output.open_stream(rate);

    let mut renderer = AudioRenderer::new(SAMPLE_RATE as f64);
    let events = build_arpeggio_events(SAMPLE_RATE);

    let total_samples = (3.0 * SAMPLE_RATE as f64) as usize;
    let mut buffer = vec![AudioFrame::default(); BLOCK_SIZE];
    let mut event_idx = 0;
    let mut sample_pos: usize = 0;

    while sample_pos < total_samples {
        let block_end = sample_pos + BLOCK_SIZE;

        while event_idx < events.len() && events[event_idx].sample < block_end {
            let ev = &events[event_idx];
            if ev.is_on {
                renderer.note_on(ev.note_id, ev.note_number, ev.velocity);
            } else {
                renderer.note_off(ev.note_id);
            }
            event_idx += 1;
        }

        renderer.render(&mut buffer);
        output.write_buffer(&stream, &buffer);

        sample_pos = block_end;
    }

    std::thread::sleep(std::time::Duration::from_secs(3));
    output.close_stream(&stream);
}

fn run_wav() {
    println!("Rendering C4-E4-G4 arpeggio to tone-test.wav...");

    let mut renderer = AudioRenderer::new(SAMPLE_RATE as f64);
    let events = build_arpeggio_events(SAMPLE_RATE);

    let total_samples = (3.0 * SAMPLE_RATE as f64) as usize;
    let mut output_frames = Vec::with_capacity(total_samples);
    let mut buffer = vec![AudioFrame::default(); BLOCK_SIZE];
    let mut event_idx = 0;
    let mut sample_pos: usize = 0;

    while sample_pos < total_samples {
        let block_end = sample_pos + BLOCK_SIZE;

        while event_idx < events.len() && events[event_idx].sample < block_end {
            let ev = &events[event_idx];
            if ev.is_on {
                renderer.note_on(ev.note_id, ev.note_number, ev.velocity);
            } else {
                renderer.note_off(ev.note_id);
            }
            event_idx += 1;
        }

        renderer.render(&mut buffer);
        let samples_this_block = BLOCK_SIZE.min(total_samples - sample_pos);
        output_frames.extend_from_slice(&buffer[..samples_this_block]);

        sample_pos = block_end;
    }

    write_wav("tone-test.wav", SAMPLE_RATE, &output_frames);
    println!("Wrote tone-test.wav ({} samples)", output_frames.len());
}

fn write_wav(path: &str, sample_rate: u32, frames: &[AudioFrame]) {
    let num_channels: u16 = 2;
    let bits_per_sample: u16 = 16;
    let byte_rate = sample_rate * num_channels as u32 * (bits_per_sample / 8) as u32;
    let block_align = num_channels * (bits_per_sample / 8);
    let data_size = frames.len() as u32 * block_align as u32;
    let file_size = 36 + data_size;

    let mut file = std::fs::File::create(path).expect("failed to create WAV file");

    file.write_all(b"RIFF").unwrap();
    file.write_all(&file_size.to_le_bytes()).unwrap();
    file.write_all(b"WAVE").unwrap();

    file.write_all(b"fmt ").unwrap();
    file.write_all(&16u32.to_le_bytes()).unwrap();
    file.write_all(&1u16.to_le_bytes()).unwrap();
    file.write_all(&num_channels.to_le_bytes()).unwrap();
    file.write_all(&sample_rate.to_le_bytes()).unwrap();
    file.write_all(&byte_rate.to_le_bytes()).unwrap();
    file.write_all(&block_align.to_le_bytes()).unwrap();
    file.write_all(&bits_per_sample.to_le_bytes()).unwrap();

    file.write_all(b"data").unwrap();
    file.write_all(&data_size.to_le_bytes()).unwrap();

    for frame in frames {
        let left = (frame.left.clamp(-1.0, 1.0) * 32767.0) as i16;
        let right = (frame.right.clamp(-1.0, 1.0) * 32767.0) as i16;
        file.write_all(&left.to_le_bytes()).unwrap();
        file.write_all(&right.to_le_bytes()).unwrap();
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let wav_mode = args.iter().any(|a| a == "--wav");

    if wav_mode {
        run_wav();
    } else {
        run_realtime();
    }
}
