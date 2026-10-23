#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ModEnvelopeConfig {
    pub attack: f64,
    pub decay: f64,
    pub sustain: f64,
    pub release: f64,
}

impl ModEnvelopeConfig {
    pub fn new(attack: f64, decay: f64, sustain: f64, release: f64) -> Result<Self, &'static str> {
        if attack < 0.0 {
            return Err("attack must be non-negative");
        }
        if decay < 0.0 {
            return Err("decay must be non-negative");
        }
        if !(0.0..=1.0).contains(&sustain) {
            return Err("sustain must be between 0.0 and 1.0");
        }
        if release < 0.0 {
            return Err("release must be non-negative");
        }
        Ok(Self { attack, decay, sustain, release })
    }
}

impl Default for ModEnvelopeConfig {
    fn default() -> Self {
        Self {
            attack: 0.01,
            decay: 0.1,
            sustain: 0.5,
            release: 0.3,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn construction_with_valid_values_succeeds() {
        let result = ModEnvelopeConfig::new(0.01, 0.1, 0.5, 0.3);
        assert!(result.is_ok());
        let config = result.unwrap();
        assert_eq!(config.attack, 0.01);
        assert_eq!(config.decay, 0.1);
        assert_eq!(config.sustain, 0.5);
        assert_eq!(config.release, 0.3);
    }

    #[test]
    fn default_values_are_correct() {
        let config = ModEnvelopeConfig::default();
        assert_eq!(config.attack, 0.01);
        assert_eq!(config.decay, 0.1);
        assert_eq!(config.sustain, 0.5);
        assert_eq!(config.release, 0.3);
    }

    #[test]
    fn negative_attack_returns_error() {
        let result = ModEnvelopeConfig::new(-0.1, 0.1, 0.5, 0.3);
        assert_eq!(result, Err("attack must be non-negative"));
    }

    #[test]
    fn negative_decay_returns_error() {
        let result = ModEnvelopeConfig::new(0.01, -0.1, 0.5, 0.3);
        assert_eq!(result, Err("decay must be non-negative"));
    }

    #[test]
    fn negative_release_returns_error() {
        let result = ModEnvelopeConfig::new(0.01, 0.1, 0.5, -0.3);
        assert_eq!(result, Err("release must be non-negative"));
    }

    #[test]
    fn sustain_below_zero_returns_error() {
        let result = ModEnvelopeConfig::new(0.01, 0.1, -0.1, 0.3);
        assert_eq!(result, Err("sustain must be between 0.0 and 1.0"));
    }

    #[test]
    fn sustain_above_one_returns_error() {
        let result = ModEnvelopeConfig::new(0.01, 0.1, 1.1, 0.3);
        assert_eq!(result, Err("sustain must be between 0.0 and 1.0"));
    }

    #[test]
    fn sustain_at_boundaries_is_valid() {
        assert!(ModEnvelopeConfig::new(0.01, 0.1, 0.0, 0.3).is_ok());
        assert!(ModEnvelopeConfig::new(0.01, 0.1, 1.0, 0.3).is_ok());
    }

    #[test]
    fn zero_attack_decay_release_is_valid() {
        assert!(ModEnvelopeConfig::new(0.0, 0.0, 0.5, 0.0).is_ok());
    }
}
