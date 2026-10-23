#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BoundaryMessageKind {
    NoteOn,
    NoteOff,
    ParameterChange,
    AllNotesOff,
}

#[derive(Debug, Clone, PartialEq)]
pub struct BoundaryMessage {
    pub kind: BoundaryMessageKind,
    pub payload: Vec<u8>,
    pub sequence_number: u64,
}

impl BoundaryMessage {
    pub fn new(kind: BoundaryMessageKind, payload: Vec<u8>, sequence_number: u64) -> Self {
        Self {
            kind,
            payload,
            sequence_number,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_note_on_message() {
        let msg = BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![60, 100], 1);
        assert_eq!(msg.kind, BoundaryMessageKind::NoteOn);
        assert_eq!(msg.payload, vec![60, 100]);
        assert_eq!(msg.sequence_number, 1);
    }

    #[test]
    fn create_parameter_change_message() {
        let msg = BoundaryMessage::new(BoundaryMessageKind::ParameterChange, vec![0, 127], 42);
        assert_eq!(msg.kind, BoundaryMessageKind::ParameterChange);
        assert_eq!(msg.sequence_number, 42);
    }

    #[test]
    fn messages_with_empty_payload() {
        let msg = BoundaryMessage::new(BoundaryMessageKind::AllNotesOff, vec![], 0);
        assert!(msg.payload.is_empty());
    }

    #[test]
    fn message_equality() {
        let a = BoundaryMessage::new(BoundaryMessageKind::NoteOff, vec![60], 5);
        let b = BoundaryMessage::new(BoundaryMessageKind::NoteOff, vec![60], 5);
        assert_eq!(a, b);
    }

    #[test]
    fn message_clone() {
        let original = BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![69, 80], 10);
        let cloned = original.clone();
        assert_eq!(original, cloned);
    }

    #[test]
    fn message_kinds_are_distinct() {
        assert_ne!(BoundaryMessageKind::NoteOn, BoundaryMessageKind::NoteOff);
        assert_ne!(BoundaryMessageKind::ParameterChange, BoundaryMessageKind::AllNotesOff);
    }
}
