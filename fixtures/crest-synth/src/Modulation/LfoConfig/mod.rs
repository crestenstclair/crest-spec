#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LfoWaveform {
    Sine,
    Triangle,
    Saw,
    Square,
    SampleAndHold,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct LfoConfig {
    pub waveform: LfoWaveform,
    pub rate: f64,
    pub depth: f64,
    pub sync_to_tempo: bool,
    pub phase: f64,
}

impl LfoConfig {
    pub fn new(
        waveform: LfoWaveform,
        rate: f64,
        depth: f64,
        sync_to_tempo: bool,
        phase: f64,
    ) -> Result<Self, &'static str> {
        if rate <= 0.0 {
            return Err("rate must be positive");
        }
        if !(0.0..=1.0).contains(&depth) {
            return Err("depth must be between 0.0 and 1.0");
        }
        Ok(Self {
            waveform,
            rate,
            depth,
            sync_to_tempo,
            phase,
        })
    }
}

impl Default for LfoConfig {
    fn default() -> Self {
        Self {
            waveform: LfoWaveform::Sine,
            rate: 1.0,
            depth: 0.5,
            sync_to_tempo: false,
            phase: 0.0,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_has_expected_values() {
        let config = LfoConfig::default();
        assert_eq!(config.waveform, LfoWaveform::Sine);
        assert_eq!(config.rate, 1.0);
        assert_eq!(config.depth, 0.5);
        assert!(!config.sync_to_tempo);
        assert_eq!(config.phase, 0.0);
    }

    #[test]
    fn construction_succeeds_with_valid_values() {
        let result = LfoConfig::new(LfoWaveform::Triangle, 2.5, 0.75, true, 0.25);
        assert!(result.is_ok());
        let config = result.unwrap();
        assert_eq!(config.waveform, LfoWaveform::Triangle);
        assert_eq!(config.rate, 2.5);
        assert_eq!(config.depth, 0.75);
        assert!(config.sync_to_tempo);
        assert_eq!(config.phase, 0.25);
    }

    #[test]
    fn construction_succeeds_with_depth_at_boundaries() {
        assert!(LfoConfig::new(LfoWaveform::Sine, 1.0, 0.0, false, 0.0).is_ok());
        assert!(LfoConfig::new(LfoWaveform::Sine, 1.0, 1.0, false, 0.0).is_ok());
    }

    #[test]
    fn construction_fails_when_rate_is_zero() {
        let result = LfoConfig::new(LfoWaveform::Sine, 0.0, 0.5, false, 0.0);
        assert_eq!(result, Err("rate must be positive"));
    }

    #[test]
    fn construction_fails_when_rate_is_negative() {
        let result = LfoConfig::new(LfoWaveform::Saw, -1.0, 0.5, false, 0.0);
        assert_eq!(result, Err("rate must be positive"));
    }

    #[test]
    fn construction_fails_when_depth_exceeds_one() {
        let result = LfoConfig::new(LfoWaveform::Square, 1.0, 1.1, false, 0.0);
        assert_eq!(result, Err("depth must be between 0.0 and 1.0"));
    }

    #[test]
    fn construction_fails_when_depth_is_negative() {
        let result = LfoConfig::new(LfoWaveform::SampleAndHold, 1.0, -0.1, false, 0.0);
        assert_eq!(result, Err("depth must be between 0.0 and 1.0"));
    }

    #[test]
    fn all_waveform_variants_are_constructible() {
        let waveforms = [
            LfoWaveform::Sine,
            LfoWaveform::Triangle,
            LfoWaveform::Saw,
            LfoWaveform::Square,
            LfoWaveform::SampleAndHold,
        ];
        for waveform in waveforms {
            assert!(LfoConfig::new(waveform, 1.0, 0.5, false, 0.0).is_ok());
        }
    }
}
