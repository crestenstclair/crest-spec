#[derive(Debug, Clone, Copy, PartialEq, PartialOrd)]
pub struct Frequency(f64);

impl Frequency {
    pub fn new(value: f64) -> Result<Self, &'static str> {
        if value <= 0.0 {
            return Err("Frequency must be positive");
        }
        Ok(Self(value))
    }

    pub fn value(self) -> f64 {
        self.0
    }
}

impl TryFrom<f64> for Frequency {
    type Error = &'static str;

    fn try_from(value: f64) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_frequency() {
        let freq = Frequency::new(440.0).unwrap();
        assert!((freq.value() - 440.0).abs() < f64::EPSILON);
    }

    #[test]
    fn rejects_zero() {
        assert!(Frequency::new(0.0).is_err());
    }

    #[test]
    fn rejects_negative() {
        assert!(Frequency::new(-1.0).is_err());
    }

    #[test]
    fn very_low_frequency() {
        let freq = Frequency::new(0.001).unwrap();
        assert!((freq.value() - 0.001).abs() < f64::EPSILON);
    }

    #[test]
    fn try_from() {
        let freq: Frequency = 880.0_f64.try_into().unwrap();
        assert!((freq.value() - 880.0).abs() < f64::EPSILON);
    }
}
