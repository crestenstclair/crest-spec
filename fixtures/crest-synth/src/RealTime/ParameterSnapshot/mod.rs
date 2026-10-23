use crate::Synth::OscillatorConfig::OscillatorConfig;
use crate::Synth::FilterConfig::FilterConfig;
use crate::Synth::AmpEnvelopeConfig::AmpEnvelopeConfig;

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ParameterSnapshot {
    pub oscillator: OscillatorConfig,
    pub filter: FilterConfig,
    pub amp_envelope: AmpEnvelopeConfig,
    pub version: u64,
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
    use crate::Synth::OscillatorConfig::Waveform;
    use crate::kernel::Frequency::Frequency;
    use crate::Synth::FilterConfig::FilterType;

    #[test]
    fn default_snapshot() {
        let snap = ParameterSnapshot::default();
        assert_eq!(snap.version, 0);
        assert_eq!(snap.oscillator, OscillatorConfig::default());
        assert_eq!(snap.filter, FilterConfig::default());
        assert_eq!(snap.amp_envelope, AmpEnvelopeConfig::default());
    }

    #[test]
    fn custom_snapshot() {
        let osc = OscillatorConfig::new(Waveform::Square, 5.0, 0.3);
        let filter = FilterConfig::new(
            Frequency::new(2000.0).unwrap(),
            0.5,
            FilterType::HighPass,
        )
        .unwrap();
        let amp = AmpEnvelopeConfig::new(0.05, 0.2, 0.6, 0.5).unwrap();

        let snap = ParameterSnapshot::new(osc, filter, amp, 42);
        assert_eq!(snap.version, 42);
        assert_eq!(snap.oscillator.waveform, Waveform::Square);
    }

    #[test]
    fn snapshot_is_copy() {
        let snap = ParameterSnapshot::default();
        let snap2 = snap;
        assert_eq!(snap, snap2);
    }
}
