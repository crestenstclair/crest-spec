use crest_synth::kernel::Frequency::{Frequency, FrequencyError};

#[test]
fn creates_frequency_from_positive_value() {
    let freq = Frequency::new(440.0).unwrap();
    assert_eq!(freq.value(), 440.0);
}

#[test]
fn creates_frequency_from_small_positive_value() {
    let freq = Frequency::new(0.001).unwrap();
    assert_eq!(freq.value(), 0.001);
}

#[test]
fn rejects_zero() {
    let result = Frequency::new(0.0);
    assert_eq!(result, Err(FrequencyError::NotPositive(0.0)));
}

#[test]
fn rejects_negative_value() {
    let result = Frequency::new(-100.0);
    assert_eq!(result, Err(FrequencyError::NotPositive(-100.0)));
}

#[test]
fn rejects_nan() {
    let result = Frequency::new(f64::NAN);
    assert!(result.is_err());
}

#[test]
fn equality_for_same_value() {
    let a = Frequency::new(440.0).unwrap();
    let b = Frequency::new(440.0).unwrap();
    assert_eq!(a, b);
}

#[test]
fn ordering() {
    let low = Frequency::new(220.0).unwrap();
    let high = Frequency::new(440.0).unwrap();
    assert!(low < high);
}

#[test]
fn copy_semantics() {
    let freq = Frequency::new(440.0).unwrap();
    let copy = freq;
    assert_eq!(freq.value(), copy.value());
}

#[test]
fn display_format() {
    let freq = Frequency::new(440.0).unwrap();
    assert_eq!(format!("{freq}"), "440 Hz");
}
