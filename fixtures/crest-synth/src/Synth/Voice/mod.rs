use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;
use crate::kernel::Frequency::Frequency;
use crate::kernel::Amplitude::Amplitude;
use crate::Synth::EnvelopeStage::EnvelopeStage;
use crate::Synth::FilterConfig::FilterState;

#[derive(Debug, Clone)]
pub enum VoiceEvent {
    VoiceActivated { note_id: NoteId, note_number: NoteNumber, frequency: Frequency },
    VoiceReleased { note_id: NoteId },
    VoiceFinished { note_id: NoteId },
    VoiceStolen { old_note_id: NoteId, new_note_id: NoteId },
}

#[derive(Debug)]
pub struct Voice {
    note_id: NoteId,
    note_number: NoteNumber,
    velocity: Velocity,
    frequency: Frequency,
    oscillator_phase: f64,
    filter_state: FilterState,
    envelope_stage: EnvelopeStage,
    envelope_level: Amplitude,
    active: bool,
}

impl Voice {
    pub fn new() -> Self {
        Self {
            note_id: NoteId::new(0),
            note_number: NoteNumber::new(0).unwrap(),
            velocity: Velocity::new(0.0).unwrap(),
            frequency: Frequency::new(440.0).unwrap(),
            oscillator_phase: 0.0,
            filter_state: FilterState::new(),
            envelope_stage: EnvelopeStage::Idle,
            envelope_level: Amplitude::silence(),
            active: false,
        }
    }

    pub fn note_on(&mut self, note_id: NoteId, note_number: NoteNumber, velocity: Velocity) -> VoiceEvent {
        let old_active = self.active;
        let old_note_id = self.note_id;

        let frequency = Frequency::new(note_number.to_frequency()).unwrap();
        self.note_id = note_id;
        self.note_number = note_number;
        self.velocity = velocity;
        self.frequency = frequency;
        self.oscillator_phase = 0.0;
        self.filter_state = FilterState::new();
        self.envelope_stage = EnvelopeStage::Attack;
        self.envelope_level = Amplitude::silence();
        self.active = true;

        if old_active {
            VoiceEvent::VoiceStolen {
                old_note_id,
                new_note_id: note_id,
            }
        } else {
            VoiceEvent::VoiceActivated {
                note_id,
                note_number,
                frequency,
            }
        }
    }

    pub fn note_off(&mut self, note_id: NoteId) -> Option<VoiceEvent> {
        if self.note_id != note_id || !self.active {
            return None;
        }
        self.envelope_stage = EnvelopeStage::Release;
        Some(VoiceEvent::VoiceReleased { note_id })
    }

    pub fn finish(&mut self) -> VoiceEvent {
        let note_id = self.note_id;
        self.envelope_stage = EnvelopeStage::Idle;
        self.envelope_level = Amplitude::silence();
        self.active = false;
        VoiceEvent::VoiceFinished { note_id }
    }

    pub fn note_id(&self) -> NoteId {
        self.note_id
    }

    pub fn note_number(&self) -> NoteNumber {
        self.note_number
    }

    pub fn velocity(&self) -> Velocity {
        self.velocity
    }

    pub fn frequency(&self) -> Frequency {
        self.frequency
    }

    pub fn oscillator_phase(&self) -> f64 {
        self.oscillator_phase
    }

    pub fn set_oscillator_phase(&mut self, phase: f64) {
        self.oscillator_phase = phase;
    }

    pub fn filter_state(&self) -> &FilterState {
        &self.filter_state
    }

    pub fn filter_state_mut(&mut self) -> &mut FilterState {
        &mut self.filter_state
    }

    pub fn envelope_stage(&self) -> EnvelopeStage {
        self.envelope_stage
    }

    pub fn set_envelope_stage(&mut self, stage: EnvelopeStage) {
        self.envelope_stage = stage;
    }

    pub fn envelope_level(&self) -> Amplitude {
        self.envelope_level
    }

    pub fn set_envelope_level(&mut self, level: Amplitude) {
        self.envelope_level = level;
    }

    pub fn is_active(&self) -> bool {
        self.active
    }

    pub fn is_reclaimable(&self) -> bool {
        self.envelope_stage == EnvelopeStage::Idle
    }
}

impl Default for Voice {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_note_id() -> NoteId {
        NoteId::new(1)
    }

    fn test_note_number() -> NoteNumber {
        NoteNumber::new(69).unwrap()
    }

    fn test_velocity() -> Velocity {
        Velocity::new(0.8).unwrap()
    }

    #[test]
    fn new_voice_is_idle() {
        let voice = Voice::new();
        assert!(!voice.is_active());
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Idle);
        assert!(voice.is_reclaimable());
    }

    #[test]
    fn note_on_activates() {
        let mut voice = Voice::new();
        let event = voice.note_on(test_note_id(), test_note_number(), test_velocity());

        assert!(voice.is_active());
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Attack);
        assert_eq!(voice.note_id(), test_note_id());
        assert!(!voice.is_reclaimable());

        match event {
            VoiceEvent::VoiceActivated { note_id, .. } => {
                assert_eq!(note_id, test_note_id());
            }
            _ => panic!("expected VoiceActivated"),
        }
    }

    #[test]
    fn note_on_while_active_steals() {
        let mut voice = Voice::new();
        voice.note_on(NoteId::new(1), test_note_number(), test_velocity());

        let event = voice.note_on(NoteId::new(2), NoteNumber::new(72).unwrap(), test_velocity());
        match event {
            VoiceEvent::VoiceStolen { old_note_id, new_note_id } => {
                assert_eq!(old_note_id, NoteId::new(1));
                assert_eq!(new_note_id, NoteId::new(2));
            }
            _ => panic!("expected VoiceStolen"),
        }
    }

    #[test]
    fn note_off_enters_release() {
        let mut voice = Voice::new();
        voice.note_on(test_note_id(), test_note_number(), test_velocity());

        let event = voice.note_off(test_note_id());
        assert!(event.is_some());
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Release);
        assert!(voice.is_active());
    }

    #[test]
    fn note_off_wrong_id_ignored() {
        let mut voice = Voice::new();
        voice.note_on(NoteId::new(1), test_note_number(), test_velocity());

        let event = voice.note_off(NoteId::new(99));
        assert!(event.is_none());
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Attack);
    }

    #[test]
    fn finish_returns_to_idle() {
        let mut voice = Voice::new();
        voice.note_on(test_note_id(), test_note_number(), test_velocity());
        voice.note_off(test_note_id());

        let event = voice.finish();
        assert!(!voice.is_active());
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Idle);
        assert!(voice.is_reclaimable());

        match event {
            VoiceEvent::VoiceFinished { note_id } => {
                assert_eq!(note_id, test_note_id());
            }
            _ => panic!("expected VoiceFinished"),
        }
    }

    #[test]
    fn frequency_derived_from_note_number() {
        let mut voice = Voice::new();
        voice.note_on(test_note_id(), NoteNumber::new(69).unwrap(), test_velocity());
        assert!((voice.frequency().value() - 440.0).abs() < 0.01);
    }
}
