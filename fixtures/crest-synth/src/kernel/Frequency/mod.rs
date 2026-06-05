use std::fmt;

#[derive(Debug, Clone, Copy, PartialEq, PartialOrd)]
pub struct Frequency(f64);

#[derive(Debug, Clone, PartialEq)]
pub enum FrequencyError {
    NotPositive(f64),
}

impl fmt::Display for FrequencyError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            FrequencyError::NotPositive(v) => {
                write!(f, "frequency must be positive, got {v}")
            }
        }
    }
}

impl std::error::Error for FrequencyError {}

impl Frequency {
    #[inline]
    pub fn new(value: f64) -> Result<Self, FrequencyError> {
        if value > 0.0 {
            Ok(Self(value))
        } else {
            Err(FrequencyError::NotPositive(value))
        }
    }

    #[inline]
    pub fn value(self) -> f64 {
        self.0
    }
}

impl fmt::Display for Frequency {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} Hz", self.0)
    }
}
