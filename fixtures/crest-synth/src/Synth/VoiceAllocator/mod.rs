use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;
use crate::Synth::Voice::{Voice, VoiceEvent};

pub struct VoiceAllocator {
    voices: Vec<Voice>,
    next_slot: usize,
}

impl VoiceAllocator {
    pub fn new(voice_count: usize) -> Self {
        let mut voices = Vec::with_capacity(voice_count);
        for _ in 0..voice_count {
            voices.push(Voice::new());
        }
        Self {
            voices,
            next_slot: 0,
        }
    }

    pub fn note_on(
        &mut self,
        note_id: NoteId,
        note_number: NoteNumber,
        velocity: Velocity,
    ) -> (usize, VoiceEvent) {
        if let Some(idx) = self.find_reclaimable() {
            let event = self.voices[idx].note_on(note_id, note_number, velocity);
            return (idx, event);
        }

        let idx = self.steal_voice();
        let event = self.voices[idx].note_on(note_id, note_number, velocity);
        (idx, event)
    }

    pub fn note_off(&mut self, note_id: NoteId) -> Option<(usize, VoiceEvent)> {
        for (idx, voice) in self.voices.iter_mut().enumerate() {
            if voice.note_id() == note_id && voice.is_active() {
                if let Some(event) = voice.note_off(note_id) {
                    return Some((idx, event));
                }
            }
        }
        None
    }

    pub fn voices(&self) -> &[Voice] {
        &self.voices
    }

    pub fn voices_mut(&mut self) -> &mut [Voice] {
        &mut self.voices
    }

    pub fn voice(&self, index: usize) -> Option<&Voice> {
        self.voices.get(index)
    }

    pub fn voice_mut(&mut self, index: usize) -> Option<&mut Voice> {
        self.voices.get_mut(index)
    }

    pub fn active_voice_count(&self) -> usize {
        self.voices.iter().filter(|v| v.is_active()).count()
    }

    fn find_reclaimable(&self) -> Option<usize> {
        self.voices.iter().position(|v| v.is_reclaimable())
    }

    fn steal_voice(&mut self) -> usize {
        let idx = self.next_slot % self.voices.len();
        self.next_slot = (self.next_slot + 1) % self.voices.len();
        idx
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn allocates_to_idle_voice() {
        let mut alloc = VoiceAllocator::new(4);
        let (idx, event) = alloc.note_on(
            NoteId::new(1),
            NoteNumber::new(60).unwrap(),
            Velocity::new(0.8).unwrap(),
        );
        assert_eq!(idx, 0);
        assert!(matches!(event, VoiceEvent::VoiceActivated { .. }));
        assert_eq!(alloc.active_voice_count(), 1);
    }

    #[test]
    fn allocates_sequentially() {
        let mut alloc = VoiceAllocator::new(4);
        for i in 0..4 {
            let (idx, _) = alloc.note_on(
                NoteId::new(i),
                NoteNumber::new(60 + i as u8).unwrap(),
                Velocity::new(0.8).unwrap(),
            );
            assert_eq!(idx, i as usize);
        }
        assert_eq!(alloc.active_voice_count(), 4);
    }

    #[test]
    fn steals_when_full() {
        let mut alloc = VoiceAllocator::new(2);
        alloc.note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(0.8).unwrap());
        alloc.note_on(NoteId::new(2), NoteNumber::new(62).unwrap(), Velocity::new(0.8).unwrap());

        let (idx, event) = alloc.note_on(
            NoteId::new(3),
            NoteNumber::new(64).unwrap(),
            Velocity::new(0.8).unwrap(),
        );
        assert_eq!(idx, 0);
        assert!(matches!(event, VoiceEvent::VoiceStolen { .. }));
    }

    #[test]
    fn note_off_releases_voice() {
        let mut alloc = VoiceAllocator::new(4);
        alloc.note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(0.8).unwrap());

        let result = alloc.note_off(NoteId::new(1));
        assert!(result.is_some());
        let (idx, event) = result.unwrap();
        assert_eq!(idx, 0);
        assert!(matches!(event, VoiceEvent::VoiceReleased { .. }));
    }

    #[test]
    fn note_off_unknown_id_returns_none() {
        let mut alloc = VoiceAllocator::new(4);
        alloc.note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(0.8).unwrap());

        let result = alloc.note_off(NoteId::new(99));
        assert!(result.is_none());
    }

    #[test]
    fn reuses_finished_voice() {
        let mut alloc = VoiceAllocator::new(2);
        alloc.note_on(NoteId::new(1), NoteNumber::new(60).unwrap(), Velocity::new(0.8).unwrap());
        alloc.note_on(NoteId::new(2), NoteNumber::new(62).unwrap(), Velocity::new(0.8).unwrap());

        alloc.voices_mut()[0].note_off(NoteId::new(1));
        alloc.voices_mut()[0].finish();

        let (idx, event) = alloc.note_on(
            NoteId::new(3),
            NoteNumber::new(64).unwrap(),
            Velocity::new(0.8).unwrap(),
        );
        assert_eq!(idx, 0);
        assert!(matches!(event, VoiceEvent::VoiceActivated { .. }));
    }

    #[test]
    fn voice_accessors() {
        let alloc = VoiceAllocator::new(4);
        assert_eq!(alloc.voices().len(), 4);
        assert!(alloc.voice(0).is_some());
        assert!(alloc.voice(4).is_none());
    }
}
