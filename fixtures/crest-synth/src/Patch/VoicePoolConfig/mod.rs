#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StealingPolicy {
    RoundRobin,
    OldestFirst,
    LowestVelocity,
}

impl Default for StealingPolicy {
    fn default() -> Self {
        StealingPolicy::RoundRobin
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct VoicePoolConfig {
    max_voices: u8,
    stealing_policy: StealingPolicy,
}

impl VoicePoolConfig {
    pub fn new(max_voices: u8, stealing_policy: StealingPolicy) -> Result<Self, &'static str> {
        if max_voices < 1 {
            return Err("maxVoices must be at least 1");
        }
        Ok(Self {
            max_voices,
            stealing_policy,
        })
    }

    pub fn max_voices(self) -> u8 {
        self.max_voices
    }

    pub fn stealing_policy(self) -> StealingPolicy {
        self.stealing_policy
    }
}

impl Default for VoicePoolConfig {
    fn default() -> Self {
        Self {
            max_voices: 16,
            stealing_policy: StealingPolicy::default(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_config() {
        let config = VoicePoolConfig::new(8, StealingPolicy::OldestFirst).unwrap();
        assert_eq!(config.max_voices(), 8);
        assert_eq!(config.stealing_policy(), StealingPolicy::OldestFirst);
    }

    #[test]
    fn rejects_zero_voices() {
        assert!(VoicePoolConfig::new(0, StealingPolicy::RoundRobin).is_err());
    }

    #[test]
    fn min_one_voice() {
        let config = VoicePoolConfig::new(1, StealingPolicy::RoundRobin).unwrap();
        assert_eq!(config.max_voices(), 1);
    }

    #[test]
    fn default_config() {
        let config = VoicePoolConfig::default();
        assert_eq!(config.max_voices(), 16);
        assert_eq!(config.stealing_policy(), StealingPolicy::RoundRobin);
    }

    #[test]
    fn all_policies() {
        let policies = [
            StealingPolicy::RoundRobin,
            StealingPolicy::OldestFirst,
            StealingPolicy::LowestVelocity,
        ];
        for policy in &policies {
            let config = VoicePoolConfig::new(4, *policy).unwrap();
            assert_eq!(config.stealing_policy(), *policy);
        }
    }
}
