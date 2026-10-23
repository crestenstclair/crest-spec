use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;
use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::SampleRate::SampleRate;

#[derive(Debug)]
pub struct SineVoice {
    note_id: NoteId,
    note_number: NoteNumber,
    frequency: f64,
    phase: f64,
    amplitude: f64,
    active: bool,
}

#[derive(Debug, Clone)]
pub enum SineVoiceEvent {
    VoiceStarted { note_id: NoteId, frequency: f64 },
    VoiceStopped { note_id: NoteId },
}

impl SineVoice {
    pub fn new(note_id: NoteId, note_number: NoteNumber, velocity: Velocity) -> Self {
        let frequency = note_number.to_frequency();
        Self {
            note_id,
            note_number,
            frequency,
            phase: 0.0,
            amplitude: velocity.value(),
            active: true,
        }
    }

    pub fn note_id(&self) -> NoteId {
        self.note_id
    }

    pub fn note_number(&self) -> NoteNumber {
        self.note_number
    }

    pub fn frequency(&self) -> f64 {
        self.frequency
    }

    pub fn is_active(&self) -> bool {
        self.active
    }

    pub fn note_off(&mut self) -> SineVoiceEvent {
        self.active = false;
        SineVoiceEvent::VoiceStopped { note_id: self.note_id }
    }

    pub fn render(&mut self, sample_rate: SampleRate) -> AudioFrame {
        if !self.active {
            return AudioFrame::silence();
        }

        let sample = (self.phase * 2.0 * std::f64::consts::PI).sin() * self.amplitude;
        self.phase += self.frequency / sample_rate.value() as f64;
        if self.phase >= 1.0 {
            self.phase -= 1.0;
        }

        let s = sample as f32;
        AudioFrame::new(s, s)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_voice() -> SineVoice {
        SineVoice::new(
            NoteId::new(1),
            NoteNumber::new(69).unwrap(),
            Velocity::new(1.0).unwrap(),
        )
    }

    #[test]
    fn new_voice_is_active() {
        let voice = make_voice();
        assert!(voice.is_active());
        assert_eq!(voice.note_id(), NoteId::new(1));
        assert!((voice.frequency() - 440.0).abs() < 0.01);
    }

    #[test]
    fn note_off_deactivates() {
        let mut voice = make_voice();
        let event = voice.note_off();
        assert!(!voice.is_active());
        match event {
            SineVoiceEvent::VoiceStopped { note_id } => {
                assert_eq!(note_id, NoteId::new(1));
            }
            _ => panic!("expected VoiceStopped"),
        }
    }

    #[test]
    fn render_produces_nonzero_output() {
        let mut voice = make_voice();
        let sr = SampleRate::new(44100).unwrap();
        let mut has_nonzero = false;
        for _ in 0..100 {
            let frame = voice.render(sr);
            if frame.left.abs() > 0.001 {
                has_nonzero = true;
                break;
            }
        }
        assert!(has_nonzero);
    }

    #[test]
    fn render_silence_when_inactive() {
        let mut voice = make_voice();
        voice.note_off();
        let sr = SampleRate::new(44100).unwrap();
        let frame = voice.render(sr);
        assert_eq!(frame.left, 0.0);
        assert_eq!(frame.right, 0.0);
    }

    #[test]
    fn phase_wraps() {
        let mut voice = make_voice();
        let sr = SampleRate::new(44100).unwrap();
        for _ in 0..44100 {
            voice.render(sr);
        }
        assert!(voice.phase >= 0.0 && voice.phase < 1.0);
    }
}
