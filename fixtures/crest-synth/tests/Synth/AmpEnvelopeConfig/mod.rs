// path: tests/Synth/AmpEnvelopeConfig/mod.rs

use crest_synth::Synth::AmpEnvelopeConfig::{AmpEnvelopeConfig, AmpEnvelopeConfigError};

#[test]
fn valid_config() {
    let cfg = AmpEnvelopeConfig::new(0.05, 0.2, 0.8, 0.4).unwrap();
    assert_eq!(cfg.attack(), 0.05);
    assert_eq!(cfg.decay(), 0.2);
    assert_eq!(cfg.sustain(), 0.8);
    assert_eq!(cfg.release(), 0.4);
}

#[test]
fn accepts_zero_times() {
    let cfg = AmpEnvelopeConfig::new(0.0, 0.0, 0.5, 0.0).unwrap();
    assert_eq!(cfg.attack(), 0.0);
    assert_eq!(cfg.decay(), 0.0);
    assert_eq!(cfg.release(), 0.0);
}

#[test]
fn accepts_sustain_zero() {
    let cfg = AmpEnvelopeConfig::new(0.01, 0.1, 0.0, 0.3).unwrap();
    assert_eq!(cfg.sustain(), 0.0);
}

#[test]
fn accepts_sustain_one() {
    let cfg = AmpEnvelopeConfig::new(0.01, 0.1, 1.0, 0.3).unwrap();
    assert_eq!(cfg.sustain(), 1.0);
}

#[test]
fn rejects_negative_attack() {
    let err = AmpEnvelopeConfig::new(-0.1, 0.1, 0.7, 0.3).unwrap_err();
    assert_eq!(err, AmpEnvelopeConfigError::NegativeTime("attack", -0.1));
}

#[test]
fn rejects_negative_decay() {
    let err = AmpEnvelopeConfig::new(0.01, -0.5, 0.7, 0.3).unwrap_err();
    assert_eq!(err, AmpEnvelopeConfigError::NegativeTime("decay", -0.5));
}

#[test]
fn rejects_negative_release() {
    let err = AmpEnvelopeConfig::new(0.01, 0.1, 0.7, -1.0).unwrap_err();
    assert_eq!(err, AmpEnvelopeConfigError::NegativeTime("release", -1.0));
}

#[test]
fn rejects_sustain_below_zero() {
    let err = AmpEnvelopeConfig::new(0.01, 0.1, -0.01, 0.3).unwrap_err();
    assert_eq!(err, AmpEnvelopeConfigError::SustainOutOfRange(-0.01));
}

#[test]
fn rejects_sustain_above_one() {
    let err = AmpEnvelopeConfig::new(0.01, 0.1, 1.01, 0.3).unwrap_err();
    assert_eq!(err, AmpEnvelopeConfigError::SustainOutOfRange(1.01));
}

#[test]
fn default_values() {
    let cfg = AmpEnvelopeConfig::default();
    assert_eq!(cfg.attack(), 0.01);
    assert_eq!(cfg.decay(), 0.1);
    assert_eq!(cfg.sustain(), 0.7);
    assert_eq!(cfg.release(), 0.3);
}

#[test]
fn copy_semantics() {
    let a = AmpEnvelopeConfig::new(0.05, 0.2, 0.8, 0.4).unwrap();
    let b = a;
    assert_eq!(a, b);
}

#[test]
fn clone_equals_original() {
    let cfg = AmpEnvelopeConfig::new(0.05, 0.2, 0.8, 0.4).unwrap();
    let cloned = cfg.clone();
    assert_eq!(cfg, cloned);
}

#[test]
fn error_display_negative_time() {
    let err = AmpEnvelopeConfigError::NegativeTime("attack", -0.5);
    let msg = format!("{err}");
    assert!(msg.contains("attack"));
    assert!(msg.contains("-0.5"));
    assert!(msg.contains("non-negative"));
}

#[test]
fn error_display_sustain_out_of_range() {
    let err = AmpEnvelopeConfigError::SustainOutOfRange(1.5);
    let msg = format!("{err}");
    assert!(msg.contains("1.5"));
    assert!(msg.contains("sustain"));
}

#[test]
fn accepts_large_times() {
    let cfg = AmpEnvelopeConfig::new(100.0, 200.0, 0.5, 300.0).unwrap();
    assert_eq!(cfg.attack(), 100.0);
    assert_eq!(cfg.decay(), 200.0);
    assert_eq!(cfg.release(), 300.0);
}
