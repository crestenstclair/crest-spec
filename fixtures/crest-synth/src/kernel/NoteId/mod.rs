#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct NoteId(u32);

impl NoteId {
    pub fn new(value: u32) -> Self {
        Self(value)
    }

    pub fn value(self) -> u32 {
        self.0
    }
}

impl From<u32> for NoteId {
    fn from(value: u32) -> Self {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_note_id() {
        let id = NoteId::new(42);
        assert_eq!(id.value(), 42);
    }

    #[test]
    fn from_u32() {
        let id: NoteId = 100u32.into();
        assert_eq!(id.value(), 100);
    }

    #[test]
    fn equality() {
        assert_eq!(NoteId::new(1), NoteId::new(1));
        assert_ne!(NoteId::new(1), NoteId::new(2));
    }
}
