use crate::kernel::Frequency::Frequency;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FilterType {
    LowPass,
    HighPass,
    BandPass,
}

impl Default for FilterType {
    fn default() -> Self {
        FilterType::LowPass
    }
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct FilterConfig {
    pub cutoff: Frequency,
    pub resonance: f64,
    pub filter_type: FilterType,
}

impl FilterConfig {
    pub fn new(cutoff: Frequency, resonance: f64, filter_type: FilterType) -> Result<Self, &'static str> {
        if !(0.0..=1.0).contains(&resonance) {
            return Err("resonance must be 0.0-1.0");
        }
        if cutoff.value() < 20.0 || cutoff.value() > 20000.0 {
            return Err("cutoff must be within audible range");
        }
        Ok(Self {
            cutoff,
            resonance,
            filter_type,
        })
    }
}

impl Default for FilterConfig {
    fn default() -> Self {
        Self {
            cutoff: Frequency::new(1000.0).unwrap(),
            resonance: 0.0,
            filter_type: FilterType::default(),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct FilterState {
    pub y1: f64,
    pub y2: f64,
}

impl FilterState {
    pub fn new() -> Self {
        Self { y1: 0.0, y2: 0.0 }
    }
}

impl Default for FilterState {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_config() {
        let cutoff = Frequency::new(1000.0).unwrap();
        let config = FilterConfig::new(cutoff, 0.5, FilterType::LowPass).unwrap();
        assert!((config.resonance - 0.5).abs() < f64::EPSILON);
        assert_eq!(config.filter_type, FilterType::LowPass);
    }

    #[test]
    fn rejects_resonance_below_zero() {
        let cutoff = Frequency::new(1000.0).unwrap();
        assert!(FilterConfig::new(cutoff, -0.1, FilterType::LowPass).is_err());
    }

    #[test]
    fn rejects_resonance_above_one() {
        let cutoff = Frequency::new(1000.0).unwrap();
        assert!(FilterConfig::new(cutoff, 1.1, FilterType::LowPass).is_err());
    }

    #[test]
    fn rejects_cutoff_below_audible() {
        let cutoff = Frequency::new(10.0).unwrap();
        assert!(FilterConfig::new(cutoff, 0.5, FilterType::LowPass).is_err());
    }

    #[test]
    fn rejects_cutoff_above_audible() {
        let cutoff = Frequency::new(25000.0).unwrap();
        assert!(FilterConfig::new(cutoff, 0.5, FilterType::LowPass).is_err());
    }

    #[test]
    fn default_config() {
        let config = FilterConfig::default();
        assert!((config.cutoff.value() - 1000.0).abs() < f64::EPSILON);
        assert!((config.resonance).abs() < f64::EPSILON);
        assert_eq!(config.filter_type, FilterType::LowPass);
    }

    #[test]
    fn filter_state_default() {
        let state = FilterState::new();
        assert!((state.y1).abs() < f64::EPSILON);
        assert!((state.y2).abs() < f64::EPSILON);
    }
}
