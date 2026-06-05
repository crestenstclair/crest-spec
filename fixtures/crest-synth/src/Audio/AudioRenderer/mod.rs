use crate::Audio::SineVoice::{SineVoice, NoteOnCommand, NoteOffCommand};
use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;
use crate::kernel::AudioFrame::AudioFrame;

const MAX_VOICES: usize = 16;

pub struct AudioRenderer {
    voices: [SineVoice; MAX_VOICES],
    sample_rate: f64,
}

impl AudioRenderer {
    pub fn new(sample_rate: f64) -> Self {
        Self {
            voices: std::array::from_fn(|_| SineVoice::new(sample_rate)),
            sample_rate,
        }
    }

    pub fn note_on(&mut self, note_id: NoteId, note_number: NoteNumber, velocity: Velocity) {
        if let Some(voice) = self.voices.iter_mut().find(|v| !v.is_active()) {
            let cmd = NoteOnCommand { note_id, note_number, velocity };
            let _ = voice.handle_note_on(cmd);
        }
    }

    pub fn note_off(&mut self, note_id: NoteId) {
        if let Some(voice) = self.voices.iter_mut().find(|v| v.is_active() && v.note_id() == note_id) {
            let cmd = NoteOffCommand { note_id };
            let _ = voice.handle_note_off(cmd);
        }
    }

    pub fn render(&mut self, buffer: &mut [AudioFrame]) {
        for frame in buffer.iter_mut() {
            *frame = AudioFrame::default();
        }
        for voice in self.voices.iter_mut() {
            if voice.is_active() {
                for frame in buffer.iter_mut() {
                    *frame += voice.render_sample();
                }
            }
        }
    }
}
