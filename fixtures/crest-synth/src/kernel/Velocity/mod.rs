#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Velocity(f64);

impl Velocity {
    pub fn new(value: f64) -> Result<Self, &'static str> {
        if !(0.0..=1.0).contains(&value) {
            return Err("Velocity must be 0.0-1.0");
        }
        Ok(Self(value))
    }

    pub fn value(self) -> f64 {
        self.0
    }

    pub fn from_midi1(midi_velocity: u8) -> Self {
        Self(midi_velocity as f64 / 127.0)
    }
}

impl TryFrom<f64> for Velocity {
    type Error = &'static str;

    fn try_from(value: f64) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_velocity() {
        assert!(Velocity::new(0.0).is_ok());
        assert!(Velocity::new(0.5).is_ok());
        assert!(Velocity::new(1.0).is_ok());
    }

    #[test]
    fn invalid_velocity() {
        assert!(Velocity::new(-0.1).is_err());
        assert!(Velocity::new(1.1).is_err());
    }

    #[test]
    fn from_midi1() {
        let vel = Velocity::from_midi1(127);
        assert!((vel.value() - 1.0).abs() < f64::EPSILON);

        let vel = Velocity::from_midi1(0);
        assert!((vel.value()).abs() < f64::EPSILON);
    }

    #[test]
    fn value_accessor() {
        let vel = Velocity::new(0.75).unwrap();
        assert!((vel.value() - 0.75).abs() < f64::EPSILON);
    }
}
