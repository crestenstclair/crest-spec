// path: src/Synth/VoiceAllocator/mod.rs

use crate::Synth::Voice::{NoteOff, NoteOn, Voice, VoiceActivated, VoiceReleased, VoiceStolen};

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum AllocationResult {
    Allocated {
        index: usize,
        event: VoiceActivated,
    },
    Stolen {
        index: usize,
        stolen: VoiceStolen,
        activated: VoiceActivated,
    },
}

// ---------------------------------------------------------------------------
// VoiceAllocator
// ---------------------------------------------------------------------------

pub struct VoiceAllocator<const N: usize> {
    voices: [Voice; N],
    /// Monotonically increasing counter stamped on each allocation.
    /// The voice with the lowest stamp is the oldest.
    ages: [u64; N],
    /// Next stamp to hand out.
    next_stamp: u64,
}

impl<const N: usize> VoiceAllocator<N> {
    /// Creates a pool of `N` idle voices.
    pub fn new() -> Self {
        Self {
            voices: core::array::from_fn(|_| Voice::new()),
            ages: [0; N],
            next_stamp: 1,
        }
    }

    /// Assigns `cmd` to an idle voice, or steals the oldest active voice.
    pub fn allocate(&mut self, cmd: NoteOn) -> AllocationResult {
        // 1. Prefer an idle voice.
        if let Some(index) = self.find_idle() {
            let event = self.voices[index].note_on(cmd);
            self.stamp(index);
            return AllocationResult::Allocated { index, event };
        }

        // 2. All voices busy -- steal the oldest.
        let index = self.find_oldest_active();
        let (stolen, activated) = self.voices[index].steal(cmd);
        self.stamp(index);
        AllocationResult::Stolen {
            index,
            stolen,
            activated,
        }
    }

    /// Releases the voice whose `note_id` matches `cmd`.
    /// Returns `None` when no voice holds that note.
    pub fn release(&mut self, cmd: NoteOff) -> Option<VoiceReleased> {
        for voice in self.voices.iter_mut() {
            if let Some(released) = voice.note_off(cmd) {
                return Some(released);
            }
        }
        None
    }

    /// Immutable accessor for a voice by index.
    #[inline]
    pub fn voice(&self, index: usize) -> &Voice {
        &self.voices[index]
    }

    /// Mutable accessor for a voice by index.
    #[inline]
    pub fn voice_mut(&mut self, index: usize) -> &mut Voice {
        &mut self.voices[index]
    }

    /// Number of currently active (sounding) voices.
    pub fn active_count(&self) -> usize {
        self.voices.iter().filter(|v| v.is_active()).count()
    }

    // -- private helpers ------------------------------------------------------

    /// Returns the index of the first idle voice, if any.
    fn find_idle(&self) -> Option<usize> {
        self.voices.iter().position(|v| v.is_idle())
    }

    /// Returns the index of the oldest active voice (lowest stamp among active voices).
    /// Panics if there are no active voices -- callers must check idle first.
    fn find_oldest_active(&self) -> usize {
        self.voices
            .iter()
            .enumerate()
            .filter(|(_, v)| v.is_active())
            .min_by_key(|(i, _)| self.ages[*i])
            .map(|(i, _)| i)
            .expect("find_oldest_active called with no active voices")
    }

    /// Stamps a voice slot with the current allocation age and advances the counter.
    fn stamp(&mut self, index: usize) {
        self.ages[index] = self.next_stamp;
        self.next_stamp += 1;
    }
}

impl<const N: usize> Default for VoiceAllocator<N> {
    fn default() -> Self {
        Self::new()
    }
}
