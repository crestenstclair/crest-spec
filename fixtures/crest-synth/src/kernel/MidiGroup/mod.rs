use core::fmt;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct MidiGroup(u8);

impl MidiGroup {
    pub const MIN: u8 = 0;
    pub const MAX: u8 = 15;

    #[inline]
    pub const fn new(value: u8) -> Result<Self, u8> {
        if value <= Self::MAX {
            Ok(Self(value))
        } else {
            Err(value)
        }
    }

    #[inline]
    pub const fn get(self) -> u8 {
        self.0
    }
}

impl TryFrom<u8> for MidiGroup {
    type Error = u8;

    #[inline]
    fn try_from(value: u8) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

impl From<MidiGroup> for u8 {
    #[inline]
    fn from(group: MidiGroup) -> u8 {
        group.0
    }
}

impl fmt::Display for MidiGroup {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}
