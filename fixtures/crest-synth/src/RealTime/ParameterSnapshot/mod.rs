use crate::Synth::AmpEnvelopeConfig::AmpEnvelopeConfig;
use crate::Synth::FilterConfig::FilterConfig;
use crate::Synth::OscillatorConfig::OscillatorConfig;

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ParameterSnapshot {
    oscillator: OscillatorConfig,
    filter: FilterConfig,
    amp_envelope: AmpEnvelopeConfig,
    version: u64,
}

impl ParameterSnapshot {
    pub fn new(
        oscillator: OscillatorConfig,
        filter: FilterConfig,
        amp_envelope: AmpEnvelopeConfig,
        version: u64,
    ) -> Self {
        Self {
            oscillator,
            filter,
            amp_envelope,
            version,
        }
    }

    pub fn oscillator(&self) -> &OscillatorConfig {
        &self.oscillator
    }

    pub fn filter(&self) -> &FilterConfig {
        &self.filter
    }

    pub fn amp_envelope(&self) -> &AmpEnvelopeConfig {
        &self.amp_envelope
    }

    pub fn version(&self) -> u64 {
        self.version
    }
}

impl Default for ParameterSnapshot {
    fn default() -> Self {
        Self {
            oscillator: OscillatorConfig::default(),
            filter: FilterConfig::default(),
            amp_envelope: AmpEnvelopeConfig::default(),
            version: 0,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_snapshot(version: u64) -> ParameterSnapshot {
        let oscillator = OscillatorConfig::default();
        let filter = FilterConfig::default();
        let amp_envelope = AmpEnvelopeConfig::default();
        ParameterSnapshot::new(oscillator, filter, amp_envelope, version)
    }

    #[test]
    fn new_stores_all_fields() {
        let snapshot = make_snapshot(42);

        assert_eq!(snapshot.version(), 42);
        assert_eq!(*snapshot.oscillator(), OscillatorConfig::default());
        assert_eq!(*snapshot.filter(), FilterConfig::default());
        assert_eq!(*snapshot.amp_envelope(), AmpEnvelopeConfig::default());
    }

    #[test]
    fn implements_copy() {
        let snapshot = make_snapshot(1);
        let copied = snapshot;
        assert_eq!(snapshot, copied);
    }

    #[test]
    fn implements_clone() {
        let snapshot = make_snapshot(7);
        let cloned = snapshot.clone();
        assert_eq!(snapshot, cloned);
    }

    #[test]
    fn equality_by_value() {
        let a = make_snapshot(10);
        let b = make_snapshot(10);
        assert_eq!(a, b);
    }

    #[test]
    fn inequality_on_version() {
        let a = make_snapshot(1);
        let b = make_snapshot(2);
        assert_ne!(a, b);
    }

    #[test]
    fn is_send_and_sync() {
        fn assert_send_sync<T: Send + Sync>() {}
        assert_send_sync::<ParameterSnapshot>();
    }

    #[test]
    fn zero_size_overhead_check() {
        let expected_max = std::mem::size_of::<OscillatorConfig>()
            + std::mem::size_of::<FilterConfig>()
            + std::mem::size_of::<AmpEnvelopeConfig>()
            + std::mem::size_of::<u64>();
        let actual = std::mem::size_of::<ParameterSnapshot>();
        assert!(
            actual <= expected_max + 64,
            "ParameterSnapshot size {actual} exceeds expected max {expected_max} + padding"
        );
    }
}
