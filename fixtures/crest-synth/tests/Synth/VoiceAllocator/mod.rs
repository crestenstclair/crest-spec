// path: tests/Synth/VoiceAllocator/mod.rs

#[cfg(test)]
mod tests {
    use crest_synth::kernel::NoteId::NoteId;
    use crest_synth::kernel::NoteNumber::NoteNumber;
    use crest_synth::kernel::Velocity::Velocity;
    use crest_synth::Synth::Voice::{NoteOff, NoteOn};
    use crest_synth::Synth::VoiceAllocator::{AllocationResult, VoiceAllocator};

    // -- helpers --------------------------------------------------------------

    fn note_on(note_id: u32, note_number: u8) -> NoteOn {
        NoteOn {
            note_id: NoteId::new(note_id),
            note_number: NoteNumber::new(note_number).unwrap(),
            velocity: Velocity::new(0.8).unwrap(),
        }
    }

    fn note_off(note_id: u32) -> NoteOff {
        NoteOff {
            note_id: NoteId::new(note_id),
        }
    }

    // -- new() ----------------------------------------------------------------

    #[test]
    fn new_allocator_has_zero_active_voices() {
        let alloc = VoiceAllocator::<8>::new();
        assert_eq!(alloc.active_count(), 0);
    }

    #[test]
    fn new_allocator_voices_are_idle() {
        let alloc = VoiceAllocator::<4>::new();
        for i in 0..4 {
            assert!(alloc.voice(i).is_idle());
        }
    }

    // -- allocate: idle voice available ---------------------------------------

    #[test]
    fn allocate_to_idle_returns_allocated() {
        let mut alloc = VoiceAllocator::<4>::new();
        let result = alloc.allocate(note_on(1, 60));
        assert!(matches!(result, AllocationResult::Allocated { .. }));
    }

    #[test]
    fn allocate_to_idle_activates_voice() {
        let mut alloc = VoiceAllocator::<4>::new();
        let result = alloc.allocate(note_on(1, 60));
        if let AllocationResult::Allocated { index, .. } = result {
            assert!(alloc.voice(index).is_active());
        }
    }

    #[test]
    fn allocate_to_idle_returns_correct_event() {
        let mut alloc = VoiceAllocator::<4>::new();
        let result = alloc.allocate(note_on(7, 69));
        if let AllocationResult::Allocated { event, .. } = result {
            assert_eq!(event.note_id, NoteId::new(7));
            assert_eq!(event.note_number, NoteNumber::new(69).unwrap());
        } else {
            panic!("expected Allocated");
        }
    }

    #[test]
    fn allocate_increases_active_count() {
        let mut alloc = VoiceAllocator::<4>::new();
        alloc.allocate(note_on(1, 60));
        assert_eq!(alloc.active_count(), 1);
        alloc.allocate(note_on(2, 62));
        assert_eq!(alloc.active_count(), 2);
    }

    #[test]
    fn allocate_fills_all_voices_without_stealing() {
        let mut alloc = VoiceAllocator::<4>::new();
        for i in 0..4 {
            let result = alloc.allocate(note_on(i as u32 + 1, 60 + i as u8));
            assert!(matches!(result, AllocationResult::Allocated { .. }));
        }
        assert_eq!(alloc.active_count(), 4);
    }

    // -- allocate: stealing ---------------------------------------------------

    #[test]
    fn allocate_steals_when_pool_is_full() {
        let mut alloc = VoiceAllocator::<2>::new();
        alloc.allocate(note_on(1, 60));
        alloc.allocate(note_on(2, 62));
        let result = alloc.allocate(note_on(3, 64));
        assert!(matches!(result, AllocationResult::Stolen { .. }));
    }

    #[test]
    fn steal_returns_correct_old_and_new_ids() {
        let mut alloc = VoiceAllocator::<2>::new();
        alloc.allocate(note_on(1, 60));
        alloc.allocate(note_on(2, 62));
        let result = alloc.allocate(note_on(3, 64));
        if let AllocationResult::Stolen {
            stolen, activated, ..
        } = result
        {
            // Oldest voice (note_id 1) should be stolen.
            assert_eq!(stolen.old_note_id, NoteId::new(1));
            assert_eq!(stolen.new_note_id, NoteId::new(3));
            assert_eq!(activated.note_id, NoteId::new(3));
        } else {
            panic!("expected Stolen");
        }
    }

    #[test]
    fn steal_targets_oldest_voice() {
        let mut alloc = VoiceAllocator::<3>::new();
        alloc.allocate(note_on(10, 60)); // oldest
        alloc.allocate(note_on(20, 62));
        alloc.allocate(note_on(30, 64));

        let result = alloc.allocate(note_on(40, 66));
        if let AllocationResult::Stolen { stolen, .. } = result {
            assert_eq!(stolen.old_note_id, NoteId::new(10));
        } else {
            panic!("expected Stolen");
        }
    }

    #[test]
    fn steal_does_not_change_active_count() {
        let mut alloc = VoiceAllocator::<2>::new();
        alloc.allocate(note_on(1, 60));
        alloc.allocate(note_on(2, 62));
        assert_eq!(alloc.active_count(), 2);
        alloc.allocate(note_on(3, 64));
        assert_eq!(alloc.active_count(), 2);
    }

    #[test]
    fn consecutive_steals_rotate_through_oldest() {
        let mut alloc = VoiceAllocator::<2>::new();
        alloc.allocate(note_on(1, 60)); // slot 0, stamp 1
        alloc.allocate(note_on(2, 62)); // slot 1, stamp 2

        // Steal oldest (slot 0, note 1)
        let r1 = alloc.allocate(note_on(3, 64));
        if let AllocationResult::Stolen { stolen, index, .. } = r1 {
            assert_eq!(stolen.old_note_id, NoteId::new(1));
            assert_eq!(index, 0);
        } else {
            panic!("expected Stolen");
        }

        // Now slot 1 (note 2) is oldest
        let r2 = alloc.allocate(note_on(4, 66));
        if let AllocationResult::Stolen { stolen, index, .. } = r2 {
            assert_eq!(stolen.old_note_id, NoteId::new(2));
            assert_eq!(index, 1);
        } else {
            panic!("expected Stolen");
        }
    }

    // -- release --------------------------------------------------------------

    #[test]
    fn release_matching_note_returns_released_event() {
        let mut alloc = VoiceAllocator::<4>::new();
        alloc.allocate(note_on(5, 60));
        let released = alloc.release(note_off(5));
        assert!(released.is_some());
        assert_eq!(released.unwrap().note_id, NoteId::new(5));
    }

    #[test]
    fn release_nonexistent_note_returns_none() {
        let mut alloc = VoiceAllocator::<4>::new();
        alloc.allocate(note_on(1, 60));
        let released = alloc.release(note_off(999));
        assert!(released.is_none());
    }

    #[test]
    fn release_on_empty_pool_returns_none() {
        let mut alloc = VoiceAllocator::<4>::new();
        assert!(alloc.release(note_off(1)).is_none());
    }

    // -- release + re-allocate ------------------------------------------------

    #[test]
    fn released_voice_becomes_idle_candidate() {
        // Voice enters Release stage via note_off, but is_idle returns false
        // because the envelope is in Release, not Idle.
        // However, once we manually mark it idle (simulating envelope completion),
        // a new allocation should prefer that slot.
        //
        // Since Voice doesn't expose a way to move to Idle from tests,
        // we verify that release doesn't change active_count (voice is
        // still active in Release).
        let mut alloc = VoiceAllocator::<2>::new();
        alloc.allocate(note_on(1, 60));
        alloc.allocate(note_on(2, 62));
        alloc.release(note_off(1));
        // Voice 1 is in Release stage -- still active, not idle.
        assert_eq!(alloc.active_count(), 2);
    }

    // -- voice accessors ------------------------------------------------------

    #[test]
    fn voice_accessor_returns_correct_voice() {
        let mut alloc = VoiceAllocator::<4>::new();
        alloc.allocate(note_on(42, 69));
        // The first allocation goes to slot 0.
        assert_eq!(alloc.voice(0).note_id(), NoteId::new(42));
    }

    #[test]
    fn voice_mut_allows_mutation() {
        let mut alloc = VoiceAllocator::<4>::new();
        alloc.allocate(note_on(1, 60));
        // Verify we can get a mutable reference.
        let voice = alloc.voice_mut(0);
        assert!(voice.is_active());
    }

    // -- single-voice pool edge case ------------------------------------------

    #[test]
    fn single_voice_pool_allocates_then_steals() {
        let mut alloc = VoiceAllocator::<1>::new();
        let r1 = alloc.allocate(note_on(1, 60));
        assert!(matches!(r1, AllocationResult::Allocated { .. }));

        let r2 = alloc.allocate(note_on(2, 62));
        if let AllocationResult::Stolen { stolen, .. } = r2 {
            assert_eq!(stolen.old_note_id, NoteId::new(1));
        } else {
            panic!("expected Stolen");
        }
    }

    // -- Default trait --------------------------------------------------------

    #[test]
    fn default_is_same_as_new() {
        let a = VoiceAllocator::<4>::new();
        let b = VoiceAllocator::<4>::default();
        assert_eq!(a.active_count(), b.active_count());
        for i in 0..4 {
            assert_eq!(a.voice(i).is_idle(), b.voice(i).is_idle());
        }
    }

    // -- allocation prefers idle over stealing --------------------------------

    #[test]
    fn allocate_prefers_idle_voice_over_stealing() {
        let mut alloc = VoiceAllocator::<4>::new();
        alloc.allocate(note_on(1, 60));
        alloc.allocate(note_on(2, 62));
        // 2 idle voices remain; should never steal.
        let result = alloc.allocate(note_on(3, 64));
        assert!(matches!(result, AllocationResult::Allocated { .. }));
    }
}
