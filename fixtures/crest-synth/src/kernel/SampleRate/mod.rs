#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct SampleRate(u32);

impl SampleRate {
    pub fn new(value: u32) -> Result<Self, &'static str> {
        if value == 0 {
            return Err("SampleRate must be positive");
        }
        Ok(Self(value))
    }

    pub fn value(self) -> u32 {
        self.0
    }
}

impl TryFrom<u32> for SampleRate {
    type Error = &'static str;

    fn try_from(value: u32) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_sample_rate() {
        let sr = SampleRate::new(44100).unwrap();
        assert_eq!(sr.value(), 44100);

        let sr = SampleRate::new(48000).unwrap();
        assert_eq!(sr.value(), 48000);
    }

    #[test]
    fn invalid_sample_rate() {
        assert!(SampleRate::new(0).is_err());
    }

    #[test]
    fn try_from() {
        let sr: SampleRate = 96000u32.try_into().unwrap();
        assert_eq!(sr.value(), 96000);
    }
}
