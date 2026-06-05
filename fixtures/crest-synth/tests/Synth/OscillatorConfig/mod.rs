// path: tests/Synth/OscillatorConfig/mod.rs

#[cfg(test)]
mod tests {
    use crest_synth::Synth::OscillatorConfig::{OscillatorConfig, Waveform};
    use std::collections::HashSet;

    #[test]
    fn default_waveform_is_sine() {
        let config = OscillatorConfig::default();
        assert_eq!(config.waveform, Waveform::Sine);
    }

    #[test]
    fn default_detune_is_zero() {
        let config = OscillatorConfig::default();
        assert_eq!(config.detune, 0.0);
    }

    #[test]
    fn default_pulse_width_is_half() {
        let config = OscillatorConfig::default();
        assert_eq!(config.pulse_width, 0.5);
    }

    #[test]
    fn waveform_default_is_sine() {
        assert_eq!(Waveform::default(), Waveform::Sine);
    }

    #[test]
    fn copy_semantics() {
        let a = OscillatorConfig::default();
        let b = a;
        assert_eq!(a, b);
    }

    #[test]
    fn clone_equals_original() {
        let config = OscillatorConfig {
            waveform: Waveform::Saw,
            detune: 7.0,
            pulse_width: 0.3,
        };
        let cloned = config.clone();
        assert_eq!(config, cloned);
    }

    #[test]
    fn different_waveforms_not_equal() {
        let a = OscillatorConfig {
            waveform: Waveform::Sine,
            ..OscillatorConfig::default()
        };
        let b = OscillatorConfig {
            waveform: Waveform::Saw,
            ..OscillatorConfig::default()
        };
        assert_ne!(a, b);
    }

    #[test]
    fn different_detune_not_equal() {
        let a = OscillatorConfig::default();
        let b = OscillatorConfig {
            detune: 12.0,
            ..OscillatorConfig::default()
        };
        assert_ne!(a, b);
    }

    #[test]
    fn different_pulse_width_not_equal() {
        let a = OscillatorConfig::default();
        let b = OscillatorConfig {
            pulse_width: 0.75,
            ..OscillatorConfig::default()
        };
        assert_ne!(a, b);
    }

    #[test]
    fn all_waveform_variants_are_distinct() {
        let variants = [
            Waveform::Sine,
            Waveform::Saw,
            Waveform::Square,
            Waveform::Triangle,
        ];

        for (i, a) in variants.iter().enumerate() {
            for (j, b) in variants.iter().enumerate() {
                if i == j {
                    assert_eq!(a, b);
                } else {
                    assert_ne!(a, b);
                }
            }
        }
    }

    #[test]
    fn waveform_hashable_in_set() {
        let mut set = HashSet::new();
        set.insert(Waveform::Sine);
        set.insert(Waveform::Sine);
        set.insert(Waveform::Square);

        assert_eq!(set.len(), 2);
        assert!(set.contains(&Waveform::Sine));
        assert!(set.contains(&Waveform::Square));
        assert!(!set.contains(&Waveform::Saw));
    }

    #[test]
    fn waveform_copy_semantics() {
        let a = Waveform::Triangle;
        let b = a;
        assert_eq!(a, b);
    }

    #[test]
    fn display_sine() {
        assert_eq!(format!("{}", Waveform::Sine), "Sine");
    }

    #[test]
    fn display_saw() {
        assert_eq!(format!("{}", Waveform::Saw), "Saw");
    }

    #[test]
    fn display_square() {
        assert_eq!(format!("{}", Waveform::Square), "Square");
    }

    #[test]
    fn display_triangle() {
        assert_eq!(format!("{}", Waveform::Triangle), "Triangle");
    }

    #[test]
    fn debug_output_waveform() {
        let debug = format!("{:?}", Waveform::Saw);
        assert_eq!(debug, "Saw");
    }

    #[test]
    fn debug_output_config() {
        let config = OscillatorConfig::default();
        let debug = format!("{:?}", config);
        assert!(debug.contains("Sine"));
        assert!(debug.contains("0.0"));
        assert!(debug.contains("0.5"));
    }

    #[test]
    fn custom_config_fields_accessible() {
        let config = OscillatorConfig {
            waveform: Waveform::Square,
            detune: -5.0,
            pulse_width: 0.25,
        };
        assert_eq!(config.waveform, Waveform::Square);
        assert_eq!(config.detune, -5.0);
        assert_eq!(config.pulse_width, 0.25);
    }

    #[test]
    fn negative_detune_allowed() {
        let config = OscillatorConfig {
            detune: -100.0,
            ..OscillatorConfig::default()
        };
        assert_eq!(config.detune, -100.0);
    }

    #[test]
    fn extreme_pulse_width_allowed() {
        let config = OscillatorConfig {
            pulse_width: 0.0,
            ..OscillatorConfig::default()
        };
        assert_eq!(config.pulse_width, 0.0);

        let config2 = OscillatorConfig {
            pulse_width: 1.0,
            ..OscillatorConfig::default()
        };
        assert_eq!(config2.pulse_width, 1.0);
    }
}
