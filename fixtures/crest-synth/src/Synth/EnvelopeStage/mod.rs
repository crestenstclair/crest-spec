use std::fmt;

/// ADSR envelope phase: Idle, Attack, Decay, Sustain, Release.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum EnvelopeStage {
    Idle,
    Attack,
    Decay,
    Sustain,
    Release,
}

impl Default for EnvelopeStage {
    fn default() -> Self {
        Self::Idle
    }
}

impl fmt::Display for EnvelopeStage {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Idle => write!(f, "Idle"),
            Self::Attack => write!(f, "Attack"),
            Self::Decay => write!(f, "Decay"),
            Self::Sustain => write!(f, "Sustain"),
            Self::Release => write!(f, "Release"),
        }
    }
}
