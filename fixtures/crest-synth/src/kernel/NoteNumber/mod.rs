use std::fmt;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct NoteNumber(u8);

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NoteNumberError {
    pub value: u8,
}

impl fmt::Display for NoteNumberError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "NoteNumber must be 0-127, got {}", self.value)
    }
}

impl std::error::Error for NoteNumberError {}

impl NoteNumber {
    pub fn new(value: u8) -> Result<Self, NoteNumberError> {
        if value > 127 {
            return Err(NoteNumberError { value });
        }
        Ok(Self(value))
    }

    #[inline]
    pub fn value(self) -> u8 {
        self.0
    }
}

impl TryFrom<u8> for NoteNumber {
    type Error = NoteNumberError;

    fn try_from(value: u8) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

impl From<NoteNumber> for u8 {
    fn from(n: NoteNumber) -> u8 {
        n.0
    }
}

impl fmt::Display for NoteNumber {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}
