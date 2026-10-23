#[derive(Debug, Clone, Copy, PartialEq)]
pub struct AmpEnvelopeConfig {
    pub attack: f64,
    pub decay: f64,
    pub sustain: f64,
    pub release: f64,
}

impl AmpEnvelopeConfig {
    pub fn new(attack: f64, decay: f64, sustain: f64, release: f64) -> Result<Self, &'static str> {
        if attack < 0.0 || decay < 0.0 || release < 0.0 {
            return Err("attack, decay, release must be non-negative");
        }
        if !(0.0..=1.0).contains(&sustain) {
            return Err("sustain must be 0.0-1.0");
        }
        Ok(Self {
            attack,
            decay,
            sustain,
            release,
        })
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_config() {
        let config = AmpEnvelopeConfig::new(0.01, 0.1, 0.7, 0.3).unwrap();
        assert!((config.attack - 0.01).abs() < f64::EPSILON);
        assert!((config.decay - 0.1).abs() < f64::EPSILON);
        assert!((config.sustain - 0.7).abs() < f64::EPSILON);
        assert!((config.release - 0.3).abs() < f64::EPSILON);
    }

    #[test]
    fn rejects_negative_attack() {
        assert!(AmpEnvelopeConfig::new(-0.1, 0.1, 0.7, 0.3).is_err());
    }

    #[test]
    fn rejects_negative_decay() {
        assert!(AmpEnvelopeConfig::new(0.01, -0.1, 0.7, 0.3).is_err());
    }

    #[test]
    fn rejects_negative_release() {
        assert!(AmpEnvelopeConfig::new(0.01, 0.1, 0.7, -0.3).is_err());
    }

    #[test]
    fn rejects_sustain_below_zero() {
        assert!(AmpEnvelopeConfig::new(0.01, 0.1, -0.1, 0.3).is_err());
    }

    #[test]
    fn rejects_sustain_above_one() {
        assert!(AmpEnvelopeConfig::new(0.01, 0.1, 1.1, 0.3).is_err());
    }

    #[test]
    fn zero_times_valid() {
        let config = AmpEnvelopeConfig::new(0.0, 0.0, 0.5, 0.0).unwrap();
        assert!((config.attack).abs() < f64::EPSILON);
    }

    #[test]
    fn default_config() {
        let config = AmpEnvelopeConfig::default();
        assert!((config.attack - 0.01).abs() < f64::EPSILON);
        assert!((config.sustain - 0.7).abs() < f64::EPSILON);
    }
}
