// path: tests/kernel/Amplitude/mod.rs

#[cfg(test)]
mod tests {
    use crest_synth::kernel::Amplitude::{Amplitude, AmplitudeError};

    #[test]
    fn silence_constant_is_zero() {
        assert_eq!(Amplitude::SILENCE.value(), 0.0);
    }

    #[test]
    fn unity_constant_is_one() {
        assert_eq!(Amplitude::UNITY.value(), 1.0);
    }

    #[test]
    fn accepts_zero() {
        let amp = Amplitude::new(0.0).unwrap();
        assert_eq!(amp.value(), 0.0);
    }

    #[test]
    fn accepts_unity() {
        let amp = Amplitude::new(1.0).unwrap();
        assert_eq!(amp.value(), 1.0);
    }

    #[test]
    fn accepts_above_unity() {
        let amp = Amplitude::new(2.5).unwrap();
        assert_eq!(amp.value(), 2.5);
    }

    #[test]
    fn rejects_negative() {
        let err = Amplitude::new(-0.1).unwrap_err();
        assert_eq!(err, AmplitudeError::Negative(-0.1));
    }

    #[test]
    fn rejects_large_negative() {
        let err = Amplitude::new(-100.0).unwrap_err();
        assert_eq!(err, AmplitudeError::Negative(-100.0));
    }

    #[test]
    fn error_display_includes_value() {
        let err = AmplitudeError::Negative(-0.5);
        let msg = format!("{err}");
        assert!(msg.contains("-0.5"));
        assert!(msg.contains("negative"));
    }

    #[test]
    fn ordering_works() {
        let low = Amplitude::new(0.2).unwrap();
        let high = Amplitude::new(0.8).unwrap();
        assert!(low < high);
    }

    #[test]
    fn copy_semantics() {
        let a = Amplitude::new(0.5).unwrap();
        let b = a;
        assert_eq!(a, b);
    }
}
