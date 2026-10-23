use std::collections::HashMap;
use std::io::Write;

use midly::{Smf, TrackEventKind, MidiMessage, Timing};

use crest_synth::kernel::MidiGroup::MidiGroup;
use crest_synth::kernel::MidiChannel::MidiChannel;
use crest_synth::kernel::ChannelAddress::ChannelAddress;
use crest_synth::kernel::NoteId::NoteId;
use crest_synth::kernel::NoteNumber::NoteNumber;
use crest_synth::kernel::Velocity::Velocity;
use crest_synth::kernel::SampleRate::SampleRate;
use crest_synth::kernel::AudioFrame::AudioFrame;
use crest_synth::Patch::PatchId::PatchId;
use crest_synth::Patch::ChannelSubscription::ChannelSubscription;
use crest_synth::Patch::Patch::{Patch, EngineType};
use crest_synth::Patch::GlobalMixer::GlobalMixer;
use crest_synth::Audio::SineVoice::SineVoice;
use crest_synth::Shell::AudioOutput::AudioOutput;
use crest_synth::Shell::CpalAudioOutput::CpalAudioOutput;

struct ChannelSynth {
    patch: Patch,
    voices: Vec<SineVoice>,
    max_voices: usize,
}

impl ChannelSynth {
    fn new(patch: Patch) -> Self {
        let max_voices = patch.voice_pool_config().max_voices() as usize;
        Self {
            patch,
            voices: Vec::new(),
            max_voices,
        }
    }

    fn note_on(&mut self, note_id: NoteId, note_number: NoteNumber, velocity: Velocity) {
        if self.voices.len() >= self.max_voices {
            self.voices.retain(|v| v.is_active());
        }
        if self.voices.len() >= self.max_voices {
            if let Some(oldest) = self.voices.first_mut() {
                oldest.note_off();
            }
            self.voices.retain(|v| v.is_active());
        }
        self.voices.push(SineVoice::new(note_id, note_number, velocity));
    }

    fn note_off(&mut self, note_number: NoteNumber) {
        for voice in &mut self.voices {
            if voice.note_number() == note_number && voice.is_active() {
                voice.note_off();
            }
        }
    }

    fn render(&mut self, sample_rate: SampleRate, output: &mut [AudioFrame]) {
        if !self.patch.is_active() {
            return;
        }
        let gain = self.patch.gain().value() as f32;
        let pan = self.patch.pan();
        let left_gain = ((1.0 - pan) * 0.5) as f32 * gain;
        let right_gain = ((1.0 + pan) * 0.5) as f32 * gain;

        for frame in output.iter_mut() {
            for voice in &mut self.voices {
                let rendered = voice.render(sample_rate);
                frame.left += rendered.left * left_gain;
                frame.right += rendered.right * right_gain;
            }
        }
        self.voices.retain(|v| v.is_active());
    }

    fn active_voice_count(&self) -> usize {
        self.voices.iter().filter(|v| v.is_active()).count()
    }
}

#[derive(Debug, Clone)]
struct TimedMidiEvent {
    time_samples: u64,
    channel: u8,
    kind: TimedEventKind,
}

#[derive(Debug, Clone)]
enum TimedEventKind {
    NoteOn { note: u8, velocity: u8 },
    NoteOff { note: u8 },
}

fn parse_midi(path: &str, sample_rate: u32) -> (Vec<TimedMidiEvent>, Vec<String>, std::collections::HashSet<u8>) {
    let data = std::fs::read(path).expect("failed to read MIDI file");
    let smf = Smf::parse(&data).expect("failed to parse MIDI file");

    let ticks_per_beat = match smf.header.timing {
        Timing::Metrical(tpb) => tpb.as_int() as f64,
        Timing::Timecode(fps, sub) => {
            let fps_val = match fps.as_int() {
                24 => 24.0,
                25 => 25.0,
                29 => 29.97,
                30 => 30.0,
                other => other as f64,
            };
            fps_val * sub as f64
        }
    };

    let mut events = Vec::new();
    let mut track_names = Vec::new();
    let mut channels = std::collections::HashSet::new();
    let mut tempo = 500_000.0_f64; // default 120 BPM

    for track in &smf.tracks {
        let mut tick: u64 = 0;
        let mut track_name = String::new();

        for event in track {
            tick += event.delta.as_int() as u64;
            let time_seconds = (tick as f64 / ticks_per_beat) * (tempo / 1_000_000.0);
            let time_samples = (time_seconds * sample_rate as f64) as u64;

            match event.kind {
                TrackEventKind::Meta(midly::MetaMessage::Tempo(t)) => {
                    tempo = t.as_int() as f64;
                }
                TrackEventKind::Meta(midly::MetaMessage::TrackName(name)) => {
                    track_name = String::from_utf8_lossy(name).to_string();
                }
                TrackEventKind::Midi { channel, message } => {
                    let ch = channel.as_int();
                    channels.insert(ch);
                    match message {
                        MidiMessage::NoteOn { key, vel } => {
                            if vel.as_int() == 0 {
                                events.push(TimedMidiEvent {
                                    time_samples,
                                    channel: ch,
                                    kind: TimedEventKind::NoteOff { note: key.as_int() },
                                });
                            } else {
                                events.push(TimedMidiEvent {
                                    time_samples,
                                    channel: ch,
                                    kind: TimedEventKind::NoteOn {
                                        note: key.as_int(),
                                        velocity: vel.as_int(),
                                    },
                                });
                            }
                        }
                        MidiMessage::NoteOff { key, .. } => {
                            events.push(TimedMidiEvent {
                                time_samples,
                                channel: ch,
                                kind: TimedEventKind::NoteOff { note: key.as_int() },
                            });
                        }
                        _ => {}
                    }
                }
                _ => {}
            }
        }

        if !track_name.is_empty() {
            track_names.push(track_name);
        }
    }

    events.sort_by_key(|e| e.time_samples);
    (events, track_names, channels)
}

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

        file.write_all(b"RIFF")?;
        file.write_all(&(36 + data_size).to_le_bytes())?;
        file.write_all(b"WAVE")?;

        file.write_all(b"fmt ")?;
        file.write_all(&16u32.to_le_bytes())?;
        file.write_all(&1u16.to_le_bytes())?;
        file.write_all(&num_channels.to_le_bytes())?;
        file.write_all(&self.sample_rate.to_le_bytes())?;
        file.write_all(&byte_rate.to_le_bytes())?;
        file.write_all(&block_align.to_le_bytes())?;
        file.write_all(&bits_per_sample.to_le_bytes())?;

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

fn create_channel_synths(channels: &std::collections::HashSet<u8>) -> HashMap<u8, ChannelSynth> {
    let mut synths = HashMap::new();
    for &ch in channels {
        let addr = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(ch).unwrap(),
        );
        let patch = Patch::new(
            PatchId::new(ch as u32),
            format!("Channel {}", ch),
            EngineType::Subtractive,
            ChannelSubscription::single(addr),
        );
        synths.insert(ch, ChannelSynth::new(patch));
    }
    synths
}

fn dispatch_events(
    synths: &mut HashMap<u8, ChannelSynth>,
    events: &[TimedMidiEvent],
    event_idx: &mut usize,
    _sample_pos: u64,
    block_end: u64,
    note_counter: &mut u32,
) {
    while *event_idx < events.len() && events[*event_idx].time_samples < block_end {
        let event = &events[*event_idx];
        if let Some(synth) = synths.get_mut(&event.channel) {
            match &event.kind {
                TimedEventKind::NoteOn { note, velocity } => {
                    if let (Ok(nn), Ok(vel)) = (
                        NoteNumber::new(*note),
                        Velocity::new(*velocity as f64 / 127.0),
                    ) {
                        *note_counter += 1;
                        synth.note_on(NoteId::new(*note_counter), nn, vel);
                    }
                }
                TimedEventKind::NoteOff { note } => {
                    if let Ok(nn) = NoteNumber::new(*note) {
                        synth.note_off(nn);
                    }
                }
            }
        }
        *event_idx += 1;
    }
}

fn run_wav_mode(midi_path: &str) {
    let sample_rate_hz = 44100u32;
    let sample_rate = SampleRate::new(sample_rate_hz).unwrap();
    let block_size = 256usize;

    let (events, track_names, channels) = parse_midi(midi_path, sample_rate_hz);

    println!("Track names:");
    for name in &track_names {
        println!("  {}", name);
    }
    println!("Channels: {}", channels.len());

    if events.is_empty() {
        println!("No MIDI events found.");
        return;
    }

    let total_samples = events.last().map(|e| e.time_samples + sample_rate_hz as u64 * 2).unwrap_or(0);

    let mut synths = create_channel_synths(&channels);
    let global_mixer = GlobalMixer::default();

    let mut wav = WavWriter::new(sample_rate_hz);
    let mut buffer = vec![AudioFrame::silence(); block_size];
    let mut sample_pos: u64 = 0;
    let mut event_idx: usize = 0;
    let mut note_counter: u32 = 0;

    while sample_pos < total_samples {
        let block_end = (sample_pos + block_size as u64).min(total_samples);
        let this_block = (block_end - sample_pos) as usize;

        for frame in buffer[..this_block].iter_mut() {
            *frame = AudioFrame::silence();
        }

        dispatch_events(&mut synths, &events, &mut event_idx, sample_pos, block_end, &mut note_counter);

        for synth in synths.values_mut() {
            synth.render(sample_rate, &mut buffer[..this_block]);
        }

        for frame in buffer[..this_block].iter_mut() {
            *frame = global_mixer.apply(*frame);
        }

        wav.extend(&buffer[..this_block]);
        sample_pos = block_end;
    }

    let output_path = "midi-test.wav";
    wav.write_to_file(output_path).expect("failed to write WAV file");
    println!("Wrote {output_path}: MIDI playback at {sample_rate_hz} Hz stereo");
}

fn run_realtime_mode(midi_path: &str) {
    let sample_rate_hz = 44100u32;
    let sample_rate = SampleRate::new(sample_rate_hz).unwrap();
    let block_size = 256usize;

    let (events, track_names, channels) = parse_midi(midi_path, sample_rate_hz);

    println!("Track names:");
    for name in &track_names {
        println!("  {}", name);
    }
    println!("Channels: {}", channels.len());

    if events.is_empty() {
        println!("No MIDI events found.");
        return;
    }

    let total_samples = events.last().map(|e| e.time_samples + sample_rate_hz as u64 * 2).unwrap_or(0);

    println!("Playing MIDI through speakers... ({:.1}s)", total_samples as f64 / sample_rate_hz as f64);

    let mut audio_output = CpalAudioOutput::new();
    let _stream = audio_output
        .open_stream(sample_rate)
        .expect("failed to open audio stream");

    let mut synths = create_channel_synths(&channels);
    let global_mixer = GlobalMixer::default();

    let mut buffer = vec![AudioFrame::silence(); block_size];
    let mut sample_pos: u64 = 0;
    let mut event_idx: usize = 0;
    let mut note_counter: u32 = 0;

    let start_time = std::time::Instant::now();

    while sample_pos < total_samples {
        let block_end = (sample_pos + block_size as u64).min(total_samples);
        let this_block = (block_end - sample_pos) as usize;

        for frame in buffer[..this_block].iter_mut() {
            *frame = AudioFrame::silence();
        }

        dispatch_events(&mut synths, &events, &mut event_idx, sample_pos, block_end, &mut note_counter);

        for synth in synths.values_mut() {
            synth.render(sample_rate, &mut buffer[..this_block]);
        }

        for frame in buffer[..this_block].iter_mut() {
            *frame = global_mixer.apply(*frame);
        }

        audio_output
            .write_buffer(&buffer[..this_block])
            .expect("failed to write audio");

        sample_pos = block_end;

        let target_time = std::time::Duration::from_secs_f64(sample_pos as f64 / sample_rate_hz as f64);
        let elapsed = start_time.elapsed();
        if target_time > elapsed {
            std::thread::sleep(target_time - elapsed);
        }
    }

    let total_voices: usize = synths.values().map(|s| s.active_voice_count()).sum();
    println!("Playback complete. Remaining active voices: {}", total_voices);
}

fn main() {
    let midi_path = "midi/Corridors of Time - Chrono Trigger.mid";
    let args: Vec<String> = std::env::args().collect();

    if args.iter().any(|a| a == "--wav") {
        run_wav_mode(midi_path);
    } else {
        run_realtime_mode(midi_path);
    }
}
