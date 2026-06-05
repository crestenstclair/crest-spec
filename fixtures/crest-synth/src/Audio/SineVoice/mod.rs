use std::f64::consts::PI;

use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;

#[derive(Debug, Clone, PartialEq)]
pub enum SineVoiceError {
    FrequencyNotPositive,
    VoiceAlreadyActive { note_id: NoteId },
    VoiceNotActive { note_id: NoteId },
}

#[derive(Debug, Clone, PartialEq)]
pub enum SineVoiceEvent {
    VoiceStarted { note_id: NoteId, frequency: f64 },
    VoiceStopped { note_id: NoteId },
}

#[derive(Debug, Clone)]
pub struct NoteOnCommand {
    pub note_id: NoteId,
    pub note_number: NoteNumber,
    pub velocity: Velocity,
}

#[derive(Debug, Clone)]
pub struct NoteOffCommand {
    pub note_id: NoteId,
}

#[derive(Debug, Clone)]
pub struct SineVoice {
    note_id: NoteId,
    note_number: NoteNumber,
    frequency: f64,
    phase: f64,
    active: bool,
    sample_rate: f64,
    velocity: f64,
}

impl SineVoice {
    pub fn new(sample_rate: f64) -> Self {
        Self {
            note_id: NoteId::new(0),
            note_number: NoteNumber::new(0).unwrap(),
            frequency: 440.0,
            phase: 0.0,
            active: false,
            sample_rate,
            velocity: 0.0,
        }
    }

    pub fn note_id(&self) -> NoteId {
        self.note_id
    }

    pub fn frequency(&self) -> f64 {
        self.frequency
    }

    pub fn phase(&self) -> f64 {
        self.phase
    }

    pub fn is_active(&self) -> bool {
        self.active
    }

    pub fn sample_rate(&self) -> f64 {
        self.sample_rate
    }

    pub fn handle_note_on(
        &mut self,
        cmd: NoteOnCommand,
    ) -> Result<Vec<SineVoiceEvent>, SineVoiceError> {
        if self.active && self.note_id == cmd.note_id {
            return Err(SineVoiceError::VoiceAlreadyActive {
                note_id: cmd.note_id,
            });
        }

        let note_number_raw = cmd.note_number.value() as f64;
        let frequency = 440.0 * 2.0_f64.powf((note_number_raw - 69.0) / 12.0);

        if frequency <= 0.0 {
            return Err(SineVoiceError::FrequencyNotPositive);
        }

        self.note_id = cmd.note_id;
        self.note_number = cmd.note_number;
        self.frequency = frequency;
        self.phase = 0.0;
        self.active = true;
        self.velocity = cmd.velocity.value();

        Ok(vec![SineVoiceEvent::VoiceStarted {
            note_id: cmd.note_id,
            frequency,
        }])
    }

    pub fn handle_note_off(
        &mut self,
        cmd: NoteOffCommand,
    ) -> Result<Vec<SineVoiceEvent>, SineVoiceError> {
        if !self.active || self.note_id != cmd.note_id {
            return Err(SineVoiceError::VoiceNotActive {
                note_id: cmd.note_id,
            });
        }

        self.active = false;
        let stopped_id = self.note_id;

        Ok(vec![SineVoiceEvent::VoiceStopped {
            note_id: stopped_id,
        }])
    }

    #[inline]
    pub fn render_sample(&mut self) -> AudioFrame {
        if !self.active {
            return AudioFrame {
                left: 0.0,
                right: 0.0,
            };
        }

        let sample = (self.phase.sin() * self.velocity) as f32;

        let phase_increment = 2.0 * PI * self.frequency / self.sample_rate;
        self.phase += phase_increment;
        if self.phase >= 2.0 * PI {
            self.phase -= 2.0 * PI;
        }

        AudioFrame {
            left: sample,
            right: sample,
        }
    }
}
