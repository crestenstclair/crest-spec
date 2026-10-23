use crate::RealTime::ParameterSnapshot::ParameterSnapshot;

pub trait ParameterBridge {
    fn write(&mut self, snapshot: ParameterSnapshot);
    fn read(&mut self) -> &ParameterSnapshot;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Synth::OscillatorConfig::{OscillatorConfig, Waveform};
    use crate::Synth::FilterConfig::FilterConfig;
    use crate::Synth::AmpEnvelopeConfig::AmpEnvelopeConfig;

    struct MockParameterBridge {
        current: ParameterSnapshot,
    }

    impl MockParameterBridge {
        fn new() -> Self {
            Self {
                current: ParameterSnapshot::default(),
            }
        }
    }

    impl ParameterBridge for MockParameterBridge {
        fn write(&mut self, snapshot: ParameterSnapshot) {
            self.current = snapshot;
        }

        fn read(&mut self) -> &ParameterSnapshot {
            &self.current
        }
    }

    #[test]
    fn write_then_read() {
        let mut bridge = MockParameterBridge::new();
        let snap = ParameterSnapshot::default();
        bridge.write(snap);
        let read = bridge.read();
        assert_eq!(read.version, 0);
    }

    #[test]
    fn read_returns_latest() {
        let mut bridge = MockParameterBridge::new();

        let snap1 = ParameterSnapshot::new(
            OscillatorConfig::default(),
            FilterConfig::default(),
            AmpEnvelopeConfig::default(),
            1,
        );
        bridge.write(snap1);

        let snap2 = ParameterSnapshot::new(
            OscillatorConfig::new(Waveform::Square, 0.0, 0.5),
            FilterConfig::default(),
            AmpEnvelopeConfig::default(),
            2,
        );
        bridge.write(snap2);

        let read = bridge.read();
        assert_eq!(read.version, 2);
    }

    #[test]
    fn default_read() {
        let mut bridge = MockParameterBridge::new();
        let read = bridge.read();
        assert_eq!(read.version, 0);
    }
}
