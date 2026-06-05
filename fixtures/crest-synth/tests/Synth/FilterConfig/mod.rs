// path: tests/Synth/FilterConfig/mod.rs

use crest_synth::kernel::Frequency::Frequency;
use crest_synth::Synth::FilterConfig::{FilterConfig, FilterConfigError, FilterType};

// --- FilterType ---

#[test]
fn filter_type_copy_semantics() {
    let a = FilterType::LowPass;
    let b = a;
    assert_eq!(a, b);
}

#[test]
fn filter_type_all_variants_distinct() {
    let variants = [FilterType::LowPass, FilterType::HighPass, FilterType::BandPass];
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
fn filter_type_hashable() {
    use std::collections::HashSet;
    let mut set = HashSet::new();
    set.insert(FilterType::LowPass);
    set.insert(FilterType::LowPass);
    set.insert(FilterType::HighPass);
    assert_eq!(set.len(), 2);
}

#[test]
fn filter_type_display() {
    assert_eq!(format!("{}", FilterType::LowPass), "LowPass");
    assert_eq!(format!("{}", FilterType::HighPass), "HighPass");
    assert_eq!(format!("{}", FilterType::BandPass), "BandPass");
}

// --- FilterConfig construction ---

#[test]
fn accepts_valid_config() {
    let cutoff = Frequency::new(440.0).unwrap();
    let config = FilterConfig::new(cutoff, 0.5, FilterType::LowPass).unwrap();
    assert_eq!(config.cutoff().value(), 440.0);
    assert_eq!(config.resonance(), 0.5);
    assert_eq!(config.filter_type(), FilterType::LowPass);
}

#[test]
fn accepts_minimum_cutoff() {
    let cutoff = Frequency::new(20.0).unwrap();
    let config = FilterConfig::new(cutoff, 0.0, FilterType::HighPass).unwrap();
    assert_eq!(config.cutoff().value(), 20.0);
}

#[test]
fn accepts_maximum_cutoff() {
    let cutoff = Frequency::new(20_000.0).unwrap();
    let config = FilterConfig::new(cutoff, 1.0, FilterType::BandPass).unwrap();
    assert_eq!(config.cutoff().value(), 20_000.0);
}

#[test]
fn accepts_zero_resonance() {
    let cutoff = Frequency::new(1000.0).unwrap();
    let config = FilterConfig::new(cutoff, 0.0, FilterType::LowPass).unwrap();
    assert_eq!(config.resonance(), 0.0);
}

#[test]
fn accepts_full_resonance() {
    let cutoff = Frequency::new(1000.0).unwrap();
    let config = FilterConfig::new(cutoff, 1.0, FilterType::LowPass).unwrap();
    assert_eq!(config.resonance(), 1.0);
}

// --- Cutoff validation ---

#[test]
fn rejects_cutoff_below_audible() {
    let cutoff = Frequency::new(19.9).unwrap();
    let err = FilterConfig::new(cutoff, 0.5, FilterType::LowPass).unwrap_err();
    assert_eq!(err, FilterConfigError::CutoffOutOfAudibleRange(19.9));
}

#[test]
fn rejects_cutoff_above_audible() {
    let cutoff = Frequency::new(20_001.0).unwrap();
    let err = FilterConfig::new(cutoff, 0.5, FilterType::LowPass).unwrap_err();
    assert_eq!(err, FilterConfigError::CutoffOutOfAudibleRange(20_001.0));
}

// --- Resonance validation ---

#[test]
fn rejects_negative_resonance() {
    let cutoff = Frequency::new(440.0).unwrap();
    let err = FilterConfig::new(cutoff, -0.1, FilterType::LowPass).unwrap_err();
    assert_eq!(err, FilterConfigError::ResonanceOutOfRange(-0.1));
}

#[test]
fn rejects_resonance_above_one() {
    let cutoff = Frequency::new(440.0).unwrap();
    let err = FilterConfig::new(cutoff, 1.01, FilterType::LowPass).unwrap_err();
    assert_eq!(err, FilterConfigError::ResonanceOutOfRange(1.01));
}

// --- Error display ---

#[test]
fn error_display_resonance() {
    let err = FilterConfigError::ResonanceOutOfRange(1.5);
    let msg = format!("{err}");
    assert!(msg.contains("1.5"));
    assert!(msg.contains("resonance"));
}

#[test]
fn error_display_cutoff() {
    let err = FilterConfigError::CutoffOutOfAudibleRange(5.0);
    let msg = format!("{err}");
    assert!(msg.contains("5"));
    assert!(msg.contains("cutoff"));
}

// --- Copy semantics ---

#[test]
fn copy_semantics() {
    let cutoff = Frequency::new(880.0).unwrap();
    let a = FilterConfig::new(cutoff, 0.7, FilterType::BandPass).unwrap();
    let b = a;
    assert_eq!(a, b);
}

// --- Each filter type constructible ---

#[test]
fn constructs_with_each_filter_type() {
    let cutoff = Frequency::new(1000.0).unwrap();
    for ft in [FilterType::LowPass, FilterType::HighPass, FilterType::BandPass] {
        let config = FilterConfig::new(cutoff, 0.5, ft).unwrap();
        assert_eq!(config.filter_type(), ft);
    }
}
