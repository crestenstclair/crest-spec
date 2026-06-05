use core::fmt;

/// ADSR envelope times (seconds) and sustain level (0.0-1.0).
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct AmpEnvelopeConfig {
    attack: f64,
    decay: f64,
    sustain: f64,
    release: f64,
}

impl AmpEnvelopeConfig {
    pub fn new(
        attack: f64,
        decay: f64,
        sustain: f64,
        release: f64,
    ) -> Result<Self, AmpEnvelopeConfigError> {
        if attack < 0.0 {
            return Err(AmpEnvelopeConfigError::NegativeTime("attack", attack));
        }
        if decay < 0.0 {
            return Err(AmpEnvelopeConfigError::NegativeTime("decay", decay));
        }
        if release < 0.0 {
            return Err(AmpEnvelopeConfigError::NegativeTime("release", release));
        }
        if !(0.0..=1.0).contains(&sustain) {
            return Err(AmpEnvelopeConfigError::SustainOutOfRange(sustain));
        }

        Ok(Self {
            attack,
            decay,
            sustain,
            release,
        })
    }

    #[inline]
    pub fn attack(self) -> f64 {
        self.attack
    }

    #[inline]
    pub fn decay(self) -> f64 {
        self.decay
    }

    #[inline]
    pub fn sustain(self) -> f64 {
        self.sustain
    }

    #[inline]
    pub fn release(self) -> f64 {
        self.release
    }
}

impl Default for AmpEnvelopeConfig {
    fn default() -> Self {
        Self {
            attack: 0.01,
            decay: 0.1,
            sustain: 0.7,
            release: 0.3,
        }
    }
}

#[derive(Debug, PartialEq)]
pub enum AmpEnvelopeConfigError {
    NegativeTime(&'static str, f64),
    SustainOutOfRange(f64),
}

impl fmt::Display for AmpEnvelopeConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NegativeTime(field, value) => {
                write!(f, "{field} time {value} must be non-negative")
            }
            Self::SustainOutOfRange(value) => {
                write!(f, "sustain level {value} is out of range [0.0, 1.0]")
            }
        }
    }
}
