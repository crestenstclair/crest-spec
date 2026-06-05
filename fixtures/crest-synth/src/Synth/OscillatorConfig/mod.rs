use std::fmt;

/// Oscillator waveform shape.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Waveform {
    Sine,
    Saw,
    Square,
    Triangle,
}

impl Default for Waveform {
    fn default() -> Self {
        Self::Sine
    }
}

impl fmt::Display for Waveform {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Sine => write!(f, "Sine"),
            Self::Saw => write!(f, "Saw"),
            Self::Square => write!(f, "Square"),
            Self::Triangle => write!(f, "Triangle"),
        }
    }
}

/// Oscillator parameters: waveform shape, detune in cents, pulse width for square.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct OscillatorConfig {
    pub waveform: Waveform,
    pub detune: f64,
    pub pulse_width: f64,
}

impl Default for OscillatorConfig {
    fn default() -> Self {
        Self {
            waveform: Waveform::Sine,
            detune: 0.0,
            pulse_width: 0.5,
        }
    }
}
