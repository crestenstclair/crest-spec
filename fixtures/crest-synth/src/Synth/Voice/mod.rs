use crate::kernel::Amplitude::Amplitude;
use crate::kernel::Frequency::Frequency;
use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;
use crate::Synth::EnvelopeStage::EnvelopeStage;

// ---------------------------------------------------------------------------
// FilterState (defined here; no pre-existing type)
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Default)]
pub struct FilterState {
    pub x1: f64,
    pub x2: f64,
    pub y1: f64,
    pub y2: f64,
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct NoteOn {
    pub note_id: NoteId,
    pub note_number: NoteNumber,
    pub velocity: Velocity,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct NoteOff {
    pub note_id: NoteId,
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct VoiceActivated {
    pub note_id: NoteId,
    pub note_number: NoteNumber,
    pub frequency: Frequency,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct VoiceReleased {
    pub note_id: NoteId,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct VoiceFinished {
    pub note_id: NoteId,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct VoiceStolen {
    pub old_note_id: NoteId,
    pub new_note_id: NoteId,
}

// ---------------------------------------------------------------------------
// Voice aggregate
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, PartialEq)]
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
    /// Creates an idle voice with zeroed state.
    pub fn new() -> Self {
        // Safe defaults: NoteId(0), NoteNumber(0), Velocity(0.0), Frequency ~8.18 Hz
        // (A0-equivalent; irrelevant because voice is idle).
        Self {
            note_id: NoteId::new(0),
            note_number: NoteNumber::new(0).expect("0 is a valid MIDI note"),
            velocity: Velocity::new(0.0).expect("0.0 is a valid velocity"),
            frequency: frequency_from_note_number(0),
            oscillator_phase: 0.0,
            filter_state: FilterState::default(),
            envelope_stage: EnvelopeStage::Idle,
            envelope_level: Amplitude::SILENCE,
            active: false,
        }
    }

    /// Activates the voice for a new note.
    pub fn note_on(&mut self, cmd: NoteOn) -> VoiceActivated {
        let freq = frequency_from_note_number(cmd.note_number.value());

        self.note_id = cmd.note_id;
        self.note_number = cmd.note_number;
        self.velocity = cmd.velocity;
        self.frequency = freq;
        self.oscillator_phase = 0.0;
        self.filter_state = FilterState::default();
        self.envelope_stage = EnvelopeStage::Attack;
        self.envelope_level = Amplitude::SILENCE;
        self.active = true;

        VoiceActivated {
            note_id: cmd.note_id,
            note_number: cmd.note_number,
            frequency: freq,
        }
    }

    /// Begins the release phase if the note id matches.
    /// Returns `None` when the ids do not match (the note-off is not for us).
    pub fn note_off(&mut self, cmd: NoteOff) -> Option<VoiceReleased> {
        if self.note_id != cmd.note_id {
            return None;
        }
        self.envelope_stage = EnvelopeStage::Release;

        Some(VoiceReleased {
            note_id: cmd.note_id,
        })
    }

    /// Whether the voice is currently sounding.
    #[inline]
    pub fn is_active(&self) -> bool {
        self.active
    }

    /// Whether the envelope has returned to `Idle` (voice is reclaimable).
    #[inline]
    pub fn is_idle(&self) -> bool {
        self.envelope_stage == EnvelopeStage::Idle
    }

    /// Steals the voice for a new note, returning both a stolen event
    /// (for the old note) and an activated event (for the new note).
    pub fn steal(&mut self, cmd: NoteOn) -> (VoiceStolen, VoiceActivated) {
        let old_note_id = self.note_id;
        let activated = self.note_on(cmd);

        let stolen = VoiceStolen {
            old_note_id,
            new_note_id: cmd.note_id,
        };

        (stolen, activated)
    }

    // -- accessors (read-only) ------------------------------------------------

    #[inline]
    pub fn note_id(&self) -> NoteId {
        self.note_id
    }

    #[inline]
    pub fn note_number(&self) -> NoteNumber {
        self.note_number
    }

    #[inline]
    pub fn velocity(&self) -> Velocity {
        self.velocity
    }

    #[inline]
    pub fn frequency(&self) -> Frequency {
        self.frequency
    }

    #[inline]
    pub fn oscillator_phase(&self) -> f64 {
        self.oscillator_phase
    }

    #[inline]
    pub fn filter_state(&self) -> FilterState {
        self.filter_state
    }

    #[inline]
    pub fn envelope_stage(&self) -> EnvelopeStage {
        self.envelope_stage
    }

    #[inline]
    pub fn envelope_level(&self) -> Amplitude {
        self.envelope_level
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

impl Default for Voice {
    fn default() -> Self {
        Self::new()
    }
}

impl Default for NoteOn {
    fn default() -> Self {
        Self {
            note_id: NoteId::new(0),
            note_number: NoteNumber::new(0).expect("0 is valid"),
            velocity: Velocity::new(0.0).expect("0.0 is valid"),
        }
    }
}

impl Default for NoteOff {
    fn default() -> Self {
        Self {
            note_id: NoteId::new(0),
        }
    }
}

/// Converts a MIDI note number to Hz using equal-temperament tuning.
/// `440.0 * 2^((note - 69) / 12)`
#[inline]
fn frequency_from_note_number(note: u8) -> Frequency {
    let hz = 440.0 * 2.0_f64.powf((note as f64 - 69.0) / 12.0);
    Frequency::new(hz).expect("MIDI note always produces positive frequency")
}
