#[derive(Debug, Clone, Copy, PartialEq, PartialOrd)]
pub struct Amplitude(f64);

impl Amplitude {
    pub fn new(value: f64) -> Result<Self, &'static str> {
        if value < 0.0 {
            return Err("Amplitude must be non-negative");
        }
        Ok(Self(value))
    }

    pub fn value(self) -> f64 {
        self.0
    }

    pub fn silence() -> Self {
        Self(0.0)
    }

    pub fn unity() -> Self {
        Self(1.0)
    }
}

impl TryFrom<f64> for Amplitude {
    type Error = &'static str;

    fn try_from(value: f64) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

impl Default for Amplitude {
    fn default() -> Self {
        Self::silence()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_amplitude() {
        assert!(Amplitude::new(0.0).is_ok());
        assert!(Amplitude::new(0.5).is_ok());
        assert!(Amplitude::new(1.0).is_ok());
        assert!(Amplitude::new(2.0).is_ok());
    }

    #[test]
    fn rejects_negative() {
        assert!(Amplitude::new(-0.1).is_err());
    }

    #[test]
    fn silence() {
        let amp = Amplitude::silence();
        assert!((amp.value()).abs() < f64::EPSILON);
    }

    #[test]
    fn unity() {
        let amp = Amplitude::unity();
        assert!((amp.value() - 1.0).abs() < f64::EPSILON);
    }

    #[test]
    fn default_is_silence() {
        let amp = Amplitude::default();
        assert_eq!(amp, Amplitude::silence());
    }

    #[test]
    fn try_from() {
        let amp: Amplitude = 0.75_f64.try_into().unwrap();
        assert!((amp.value() - 0.75).abs() < f64::EPSILON);
    }
}
