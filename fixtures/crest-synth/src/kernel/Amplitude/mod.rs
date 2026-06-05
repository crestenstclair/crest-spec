/// Linear amplitude (0.0 = silence, 1.0 = unity).
///
/// Wraps an `f64` that must be non-negative. Values above 1.0 are permitted
/// (representing gain above unity) but negative values are rejected.
#[derive(Debug, Clone, Copy, PartialEq, PartialOrd)]
pub struct Amplitude(f64);

impl Amplitude {
    pub fn new(value: f64) -> Result<Self, AmplitudeError> {
        if value >= 0.0 {
            Ok(Self(value))
        } else {
            Err(AmplitudeError::Negative(value))
        }
    }

    /// Silence (0.0).
    pub const SILENCE: Self = Self(0.0);

    /// Unity gain (1.0).
    pub const UNITY: Self = Self(1.0);

    #[inline]
    pub fn value(self) -> f64 {
        self.0
    }
}

#[derive(Debug, PartialEq)]
pub enum AmplitudeError {
    Negative(f64),
}

impl core::fmt::Display for AmplitudeError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            Self::Negative(v) => write!(f, "amplitude {v} is negative"),
        }
    }
}
