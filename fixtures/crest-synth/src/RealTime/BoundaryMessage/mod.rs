/// Discriminates the type of message crossing the real-time boundary.
///
/// Each variant represents a distinct control or musical event that the
/// non-RT side can send to (or receive from) the audio thread via a
/// lock-free ring buffer.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum BoundaryMessageKind {
    /// MIDI note-on: payload carries note number + velocity.
    NoteOn = 0,
    /// MIDI note-off: payload carries note number + release velocity.
    NoteOff = 1,
    /// Continuous controller change (filter cutoff, resonance, etc.).
    ParameterChange = 2,
    /// Pitch-bend wheel value.
    PitchBend = 3,
    /// Channel aftertouch / pressure.
    Aftertouch = 4,
    /// Mod-wheel or other modulation source update.
    ModWheel = 5,
    /// Transport command (start / stop / seek).
    Transport = 6,
    /// Preset / program-change notification.
    ProgramChange = 7,
    /// Request to reset all voices (panic / all-notes-off).
    AllNotesOff = 8,
    /// Tempo change expressed in BPM (payload is f64 bytes).
    TempoChange = 9,
}

/// A discrete message that crosses the real-time boundary via a lock-free
/// ring buffer.
///
/// `BoundaryMessage` is a value object: two messages with the same kind,
/// payload and sequence number are considered equal.  The `sequence_number`
/// provides total ordering so the consumer can detect drops or reorder.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct BoundaryMessage {
    kind: BoundaryMessageKind,
    payload: Vec<u8>,
    sequence_number: u64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum BoundaryMessageError {
    EmptyPayload,
    PayloadTooLarge(usize),
}

impl core::fmt::Display for BoundaryMessageError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            Self::EmptyPayload => write!(f, "boundary message payload must not be empty"),
            Self::PayloadTooLarge(len) => {
                write!(f, "boundary message payload of {len} bytes exceeds the 256-byte limit")
            }
        }
    }
}

/// Maximum payload size.  Keeps messages small enough to fit in typical
/// lock-free ring buffer slots without fragmentation.
const MAX_PAYLOAD_LEN: usize = 256;

impl BoundaryMessage {
    /// Creates a new `BoundaryMessage`, validating the payload size.
    pub fn new(
        kind: BoundaryMessageKind,
        payload: Vec<u8>,
        sequence_number: u64,
    ) -> Result<Self, BoundaryMessageError> {
        if payload.is_empty() {
            return Err(BoundaryMessageError::EmptyPayload);
        }
        if payload.len() > MAX_PAYLOAD_LEN {
            return Err(BoundaryMessageError::PayloadTooLarge(payload.len()));
        }
        Ok(Self {
            kind,
            payload,
            sequence_number,
        })
    }

    #[inline]
    pub fn kind(&self) -> BoundaryMessageKind {
        self.kind
    }

    #[inline]
    pub fn payload(&self) -> &[u8] {
        &self.payload
    }

    #[inline]
    pub fn sequence_number(&self) -> u64 {
        self.sequence_number
    }
}

impl BoundaryMessageKind {
    /// Returns the `u8` discriminant for wire-format serialization.
    #[inline]
    pub fn as_u8(self) -> u8 {
        self as u8
    }

    /// Reconstructs a kind from its wire-format discriminant.
    pub fn from_u8(value: u8) -> Option<Self> {
        match value {
            0 => Some(Self::NoteOn),
            1 => Some(Self::NoteOff),
            2 => Some(Self::ParameterChange),
            3 => Some(Self::PitchBend),
            4 => Some(Self::Aftertouch),
            5 => Some(Self::ModWheel),
            6 => Some(Self::Transport),
            7 => Some(Self::ProgramChange),
            8 => Some(Self::AllNotesOff),
            9 => Some(Self::TempoChange),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // ── Construction ────────────────────────────────────────────────

    #[test]
    fn new_creates_valid_message() {
        let msg = BoundaryMessage::new(
            BoundaryMessageKind::NoteOn,
            vec![60, 100],
            1,
        )
        .expect("valid message");

        assert_eq!(msg.kind(), BoundaryMessageKind::NoteOn);
        assert_eq!(msg.payload(), &[60, 100]);
        assert_eq!(msg.sequence_number(), 1);
    }

    #[test]
    fn new_rejects_empty_payload() {
        let result = BoundaryMessage::new(
            BoundaryMessageKind::NoteOff,
            vec![],
            0,
        );
        assert_eq!(result, Err(BoundaryMessageError::EmptyPayload));
    }

    #[test]
    fn new_rejects_oversized_payload() {
        let big = vec![0u8; MAX_PAYLOAD_LEN + 1];
        let result = BoundaryMessage::new(
            BoundaryMessageKind::ParameterChange,
            big.clone(),
            0,
        );
        assert_eq!(
            result,
            Err(BoundaryMessageError::PayloadTooLarge(big.len()))
        );
    }

    #[test]
    fn new_accepts_max_size_payload() {
        let payload = vec![0xAB; MAX_PAYLOAD_LEN];
        let msg = BoundaryMessage::new(
            BoundaryMessageKind::PitchBend,
            payload.clone(),
            42,
        )
        .expect("max-size payload is valid");

        assert_eq!(msg.payload().len(), MAX_PAYLOAD_LEN);
    }

    // ── Value-object equality ───────────────────────────────────────

    #[test]
    fn equal_messages_are_equal() {
        let a = BoundaryMessage::new(
            BoundaryMessageKind::NoteOn,
            vec![60, 127],
            5,
        )
        .unwrap();

        let b = BoundaryMessage::new(
            BoundaryMessageKind::NoteOn,
            vec![60, 127],
            5,
        )
        .unwrap();

        assert_eq!(a, b);
    }

    #[test]
    fn different_kind_means_not_equal() {
        let a = BoundaryMessage::new(
            BoundaryMessageKind::NoteOn,
            vec![60],
            0,
        )
        .unwrap();

        let b = BoundaryMessage::new(
            BoundaryMessageKind::NoteOff,
            vec![60],
            0,
        )
        .unwrap();

        assert_ne!(a, b);
    }

    #[test]
    fn different_sequence_means_not_equal() {
        let a = BoundaryMessage::new(
            BoundaryMessageKind::NoteOn,
            vec![60],
            1,
        )
        .unwrap();

        let b = BoundaryMessage::new(
            BoundaryMessageKind::NoteOn,
            vec![60],
            2,
        )
        .unwrap();

        assert_ne!(a, b);
    }

    #[test]
    fn different_payload_means_not_equal() {
        let a = BoundaryMessage::new(
            BoundaryMessageKind::ParameterChange,
            vec![1, 2, 3],
            0,
        )
        .unwrap();

        let b = BoundaryMessage::new(
            BoundaryMessageKind::ParameterChange,
            vec![4, 5, 6],
            0,
        )
        .unwrap();

        assert_ne!(a, b);
    }

    // ── Clone ───────────────────────────────────────────────────────

    #[test]
    fn clone_produces_equal_message() {
        let original = BoundaryMessage::new(
            BoundaryMessageKind::ModWheel,
            vec![0x7F],
            99,
        )
        .unwrap();

        let cloned = original.clone();
        assert_eq!(original, cloned);
    }

    // ── BoundaryMessageKind round-trip ──────────────────────────────

    #[test]
    fn kind_round_trips_through_u8() {
        let variants = [
            BoundaryMessageKind::NoteOn,
            BoundaryMessageKind::NoteOff,
            BoundaryMessageKind::ParameterChange,
            BoundaryMessageKind::PitchBend,
            BoundaryMessageKind::Aftertouch,
            BoundaryMessageKind::ModWheel,
            BoundaryMessageKind::Transport,
            BoundaryMessageKind::ProgramChange,
            BoundaryMessageKind::AllNotesOff,
            BoundaryMessageKind::TempoChange,
        ];

        for variant in variants {
            let wire = variant.as_u8();
            let recovered = BoundaryMessageKind::from_u8(wire)
                .unwrap_or_else(|| panic!("failed to round-trip {:?} (wire = {})", variant, wire));
            assert_eq!(variant, recovered);
        }
    }

    #[test]
    fn from_u8_returns_none_for_unknown_discriminant() {
        assert_eq!(BoundaryMessageKind::from_u8(10), None);
        assert_eq!(BoundaryMessageKind::from_u8(255), None);
    }

    // ── Display for errors ──────────────────────────────────────────

    #[test]
    fn error_display_empty_payload() {
        let err = BoundaryMessageError::EmptyPayload;
        let text = format!("{err}");
        assert!(text.contains("must not be empty"), "got: {text}");
    }

    #[test]
    fn error_display_payload_too_large() {
        let err = BoundaryMessageError::PayloadTooLarge(512);
        let text = format!("{err}");
        assert!(text.contains("512"), "got: {text}");
        assert!(text.contains("256"), "got: {text}");
    }

    // ── All message kinds can be constructed ────────────────────────

    #[test]
    fn all_kinds_construct_successfully() {
        let kinds = [
            BoundaryMessageKind::NoteOn,
            BoundaryMessageKind::NoteOff,
            BoundaryMessageKind::ParameterChange,
            BoundaryMessageKind::PitchBend,
            BoundaryMessageKind::Aftertouch,
            BoundaryMessageKind::ModWheel,
            BoundaryMessageKind::Transport,
            BoundaryMessageKind::ProgramChange,
            BoundaryMessageKind::AllNotesOff,
            BoundaryMessageKind::TempoChange,
        ];

        for (i, kind) in kinds.iter().enumerate() {
            let msg = BoundaryMessage::new(*kind, vec![0x42], i as u64)
                .unwrap_or_else(|e| panic!("failed to create {:?}: {e}", kind));
            assert_eq!(msg.kind(), *kind);
            assert_eq!(msg.sequence_number(), i as u64);
        }
    }

    // ── Hash consistency ────────────────────────────────────────────

    #[test]
    fn equal_messages_have_equal_hashes() {
        use std::collections::hash_map::DefaultHasher;
        use std::hash::{Hash, Hasher};

        let a = BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![60], 1).unwrap();
        let b = BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![60], 1).unwrap();

        let hash_of = |msg: &BoundaryMessage| {
            let mut h = DefaultHasher::new();
            msg.hash(&mut h);
            h.finish()
        };

        assert_eq!(hash_of(&a), hash_of(&b));
    }
}
