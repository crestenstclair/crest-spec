use std::fmt;

use crate::kernel::Frequency::Frequency;

const MIN_AUDIBLE_HZ: f64 = 20.0;
const MAX_AUDIBLE_HZ: f64 = 20_000.0;

/// Resonant filter type selector.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum FilterType {
    LowPass,
    HighPass,
    BandPass,
}

impl fmt::Display for FilterType {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::LowPass => write!(f, "LowPass"),
            Self::HighPass => write!(f, "HighPass"),
            Self::BandPass => write!(f, "BandPass"),
        }
    }
}

/// Resonant filter parameters.
///
/// Invariants:
/// - `resonance` must be in `[0.0, 1.0]`
/// - `cutoff` must be within the audible range (20 Hz - 20 kHz)
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct FilterConfig {
    cutoff: Frequency,
    resonance: f64,
    filter_type: FilterType,
}

impl FilterConfig {
    pub fn new(
        cutoff: Frequency,
        resonance: f64,
        filter_type: FilterType,
    ) -> Result<Self, FilterConfigError> {
        let hz = cutoff.value();
        if hz < MIN_AUDIBLE_HZ || hz > MAX_AUDIBLE_HZ {
            return Err(FilterConfigError::CutoffOutOfAudibleRange(hz));
        }
        if resonance < 0.0 || resonance > 1.0 {
            return Err(FilterConfigError::ResonanceOutOfRange(resonance));
        }
        Ok(Self {
            cutoff,
            resonance,
            filter_type,
        })
    }

    #[inline]
    pub fn cutoff(self) -> Frequency {
        self.cutoff
    }

    #[inline]
    pub fn resonance(self) -> f64 {
        self.resonance
    }

    #[inline]
    pub fn filter_type(self) -> FilterType {
        self.filter_type
    }
}

#[derive(Debug, PartialEq)]
pub enum FilterConfigError {
    ResonanceOutOfRange(f64),
    CutoffOutOfAudibleRange(f64),
}

impl Default for FilterConfig {
    fn default() -> Self {
        Self {
            cutoff: Frequency::new(1000.0).unwrap(),
            resonance: 0.0,
            filter_type: FilterType::LowPass,
        }
    }
}

impl fmt::Display for FilterConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::ResonanceOutOfRange(v) => {
                write!(f, "resonance {v} is out of range [0.0, 1.0]")
            }
            Self::CutoffOutOfAudibleRange(hz) => {
                write!(f, "cutoff {hz} Hz is outside audible range [20.0, 20000.0]")
            }
        }
    }
}
