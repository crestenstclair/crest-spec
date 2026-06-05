use std::fmt;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct MidiChannel(u8);

impl MidiChannel {
    pub fn new(value: u8) -> Result<Self, MidiChannelError> {
        if value > 15 {
            return Err(MidiChannelError::OutOfRange(value));
        }
        Ok(Self(value))
    }

    pub fn value(self) -> u8 {
        self.0
    }
}

impl TryFrom<u8> for MidiChannel {
    type Error = MidiChannelError;

    fn try_from(value: u8) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

impl From<MidiChannel> for u8 {
    fn from(channel: MidiChannel) -> u8 {
        channel.0
    }
}

impl fmt::Display for MidiChannel {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.0)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MidiChannelError {
    OutOfRange(u8),
}

impl fmt::Display for MidiChannelError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            MidiChannelError::OutOfRange(v) => {
                write!(f, "MIDI channel {} is out of range (must be 0-15)", v)
            }
        }
    }
}

impl std::error::Error for MidiChannelError {}
