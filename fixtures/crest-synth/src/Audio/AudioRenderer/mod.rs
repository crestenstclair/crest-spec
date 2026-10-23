use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;
use crate::kernel::SampleRate::SampleRate;
use crate::kernel::AudioFrame::AudioFrame;
use crate::Audio::SineVoice::SineVoice;

const MAX_VOICES: usize = 64;

pub struct AudioRenderer {
    voices: Vec<SineVoice>,
    sample_rate: SampleRate,
}

impl AudioRenderer {
    pub fn new(sample_rate: SampleRate) -> Self {
        Self {
            voices: Vec::with_capacity(MAX_VOICES),
            sample_rate,
        }
    }

    pub fn note_on(&mut self, note_id: NoteId, note_number: NoteNumber, velocity: Velocity) {
        if self.voices.len() >= MAX_VOICES {
            self.voices.retain(|v| v.is_active());
        }
        if self.voices.len() < MAX_VOICES {
            let voice = SineVoice::new(note_id, note_number, velocity);
            self.voices.push(voice);
        }
    }

    pub fn note_off(&mut self, note_id: NoteId) {
        for voice in &mut self.voices {
            if voice.note_id() == note_id && voice.is_active() {
                voice.note_off();
            }
        }
    }

    pub fn render_block(&mut self, buffer: &mut [AudioFrame]) {
        for frame in buffer.iter_mut() {
            *frame = AudioFrame::silence();
        }

        for frame in buffer.iter_mut() {
            for voice in &mut self.voices {
                *frame += voice.render(self.sample_rate);
            }
        }

        self.voices.retain(|v| v.is_active());
    }

    pub fn active_voice_count(&self) -> usize {
        self.voices.iter().filter(|v| v.is_active()).count()
    }

    pub fn sample_rate(&self) -> SampleRate {
        self.sample_rate
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn renderer() -> AudioRenderer {
        AudioRenderer::new(SampleRate::new(44100).unwrap())
    }

    #[test]
    fn starts_empty() {
        let r = renderer();
        assert_eq!(r.active_voice_count(), 0);
    }

    #[test]
    fn note_on_adds_voice() {
        let mut r = renderer();
        r.note_on(
            NoteId::new(1),
            NoteNumber::new(60).unwrap(),
            Velocity::new(0.8).unwrap(),
        );
        assert_eq!(r.active_voice_count(), 1);
    }

    #[test]
    fn note_off_deactivates_voice() {
        let mut r = renderer();
        r.note_on(
            NoteId::new(1),
            NoteNumber::new(60).unwrap(),
            Velocity::new(0.8).unwrap(),
        );
        r.note_off(NoteId::new(1));
        assert_eq!(r.active_voice_count(), 0);
    }

    #[test]
    fn render_produces_nonzero_output() {
        let mut r = renderer();
        r.note_on(
            NoteId::new(1),
            NoteNumber::new(69).unwrap(),
            Velocity::new(1.0).unwrap(),
        );

        let mut buffer = [AudioFrame::silence(); 256];
        r.render_block(&mut buffer);

        let has_signal = buffer.iter().any(|f| f.left.abs() > 0.001);
        assert!(has_signal);
    }

    #[test]
    fn render_silence_when_empty() {
        let mut r = renderer();
        let mut buffer = [AudioFrame::silence(); 64];
        r.render_block(&mut buffer);

        for frame in &buffer {
            assert_eq!(frame.left, 0.0);
            assert_eq!(frame.right, 0.0);
        }
    }

    #[test]
    fn inactive_voices_are_cleaned_after_render() {
        let mut r = renderer();
        r.note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(0.5).unwrap());
        r.note_off(NoteId::new(1));

        let mut buffer = [AudioFrame::silence(); 64];
        r.render_block(&mut buffer);

        assert_eq!(r.voices.len(), 0);
    }

    #[test]
    fn multiple_voices_mix() {
        let mut r = renderer();
        r.note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(0.5).unwrap());
        r.note_on(NoteId::new(2), NoteNumber::new(64).unwrap(), Velocity::new(0.5).unwrap());
        assert_eq!(r.active_voice_count(), 2);

        let mut buffer = [AudioFrame::silence(); 256];
        r.render_block(&mut buffer);

        let has_signal = buffer.iter().any(|f| f.left.abs() > 0.001);
        assert!(has_signal);
    }
}
