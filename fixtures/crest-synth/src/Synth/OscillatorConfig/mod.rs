#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Waveform {
    Sine,
    Saw,
    Square,
    Triangle,
}

impl Default for Waveform {
    fn default() -> Self {
        Waveform::Saw
    }
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct OscillatorConfig {
    pub waveform: Waveform,
    pub detune: f64,
    pub pulse_width: f64,
}

impl OscillatorConfig {
    pub fn new(waveform: Waveform, detune: f64, pulse_width: f64) -> Self {
        Self {
            waveform,
            detune,
            pulse_width,
        }
    }
}

impl Default for OscillatorConfig {
    fn default() -> Self {
        Self {
            waveform: Waveform::default(),
            detune: 0.0,
            pulse_width: 0.5,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_config() {
        let config = OscillatorConfig::default();
        assert_eq!(config.waveform, Waveform::Saw);
        assert!((config.detune).abs() < f64::EPSILON);
        assert!((config.pulse_width - 0.5).abs() < f64::EPSILON);
    }

    #[test]
    fn custom_config() {
        let config = OscillatorConfig::new(Waveform::Square, 5.0, 0.3);
        assert_eq!(config.waveform, Waveform::Square);
        assert!((config.detune - 5.0).abs() < f64::EPSILON);
        assert!((config.pulse_width - 0.3).abs() < f64::EPSILON);
    }

    #[test]
    fn waveform_default_is_saw() {
        assert_eq!(Waveform::default(), Waveform::Saw);
    }

    #[test]
    fn all_waveforms() {
        let waveforms = [Waveform::Sine, Waveform::Saw, Waveform::Square, Waveform::Triangle];
        assert_eq!(waveforms.len(), 4);
    }
}
