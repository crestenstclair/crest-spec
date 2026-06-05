#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct NoteId(u32);

impl NoteId {
    #[inline]
    pub fn new(value: u32) -> Self {
        Self(value)
    }

    #[inline]
    pub fn value(self) -> u32 {
        self.0
    }
}

impl From<u32> for NoteId {
    #[inline]
    fn from(value: u32) -> Self {
        Self::new(value)
    }
}

impl From<NoteId> for u32 {
    #[inline]
    fn from(id: NoteId) -> u32 {
        id.0
    }
}

impl core::fmt::Display for NoteId {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        write!(f, "NoteId({})", self.0)
    }
}
