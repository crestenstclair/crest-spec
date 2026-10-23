#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EnvelopeStage {
    Idle,
    Attack,
    Decay,
    Sustain,
    Release,
}

impl EnvelopeStage {
    pub fn is_active(self) -> bool {
        !matches!(self, EnvelopeStage::Idle)
    }
}

impl Default for EnvelopeStage {
    fn default() -> Self {
        EnvelopeStage::Idle
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn idle_is_not_active() {
        assert!(!EnvelopeStage::Idle.is_active());
    }

    #[test]
    fn attack_is_active() {
        assert!(EnvelopeStage::Attack.is_active());
    }

    #[test]
    fn decay_is_active() {
        assert!(EnvelopeStage::Decay.is_active());
    }

    #[test]
    fn sustain_is_active() {
        assert!(EnvelopeStage::Sustain.is_active());
    }

    #[test]
    fn release_is_active() {
        assert!(EnvelopeStage::Release.is_active());
    }

    #[test]
    fn default_is_idle() {
        assert_eq!(EnvelopeStage::default(), EnvelopeStage::Idle);
    }
}
