use std::fmt;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct SampleRate(u32);

impl SampleRate {
    pub fn new(value: u32) -> Result<Self, SampleRateError> {
        if value == 0 {
            return Err(SampleRateError::MustBePositive);
        }
        Ok(Self(value))
    }

    pub fn value(self) -> u32 {
        self.0
    }
}

impl fmt::Display for SampleRate {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} Hz", self.0)
    }
}

impl TryFrom<u32> for SampleRate {
    type Error = SampleRateError;

    fn try_from(value: u32) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

impl From<SampleRate> for u32 {
    fn from(sample_rate: SampleRate) -> Self {
        sample_rate.0
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SampleRateError {
    MustBePositive,
}

impl fmt::Display for SampleRateError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::MustBePositive => write!(f, "sample rate must be positive"),
        }
    }
}

impl std::error::Error for SampleRateError {}
