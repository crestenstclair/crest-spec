#[derive(Debug, Clone, Copy, PartialEq, PartialOrd)]
pub struct Velocity(f64);

impl Velocity {
    pub fn new(value: f64) -> Result<Self, VelocityError> {
        if value >= 0.0 && value <= 1.0 {
            Ok(Self(value))
        } else {
            Err(VelocityError::OutOfRange(value))
        }
    }

    pub fn from_midi(midi: u8) -> Result<Self, VelocityError> {
        if midi > 127 {
            return Err(VelocityError::InvalidMidiByte(midi));
        }
        Ok(Self(midi as f64 / 127.0))
    }

    #[inline]
    pub fn value(self) -> f64 {
        self.0
    }

    #[inline]
    pub fn to_midi(self) -> u8 {
        (self.0 * 127.0).round() as u8
    }
}

#[derive(Debug, PartialEq)]
pub enum VelocityError {
    OutOfRange(f64),
    InvalidMidiByte(u8),
}

impl core::fmt::Display for VelocityError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            Self::OutOfRange(v) => write!(f, "velocity {v} is out of range [0.0, 1.0]"),
            Self::InvalidMidiByte(b) => write!(f, "MIDI byte {b} exceeds 127"),
        }
    }
}
