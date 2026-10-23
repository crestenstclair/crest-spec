#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct MidiChannel(u8);

impl MidiChannel {
    pub fn new(value: u8) -> Result<Self, &'static str> {
        if value > 15 {
            return Err("MidiChannel must be 0-15");
        }
        Ok(Self(value))
    }

    pub fn value(self) -> u8 {
        self.0
    }
}

impl TryFrom<u8> for MidiChannel {
    type Error = &'static str;

    fn try_from(value: u8) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_channel() {
        for i in 0..=15 {
            let ch = MidiChannel::new(i).unwrap();
            assert_eq!(ch.value(), i);
        }
    }

    #[test]
    fn invalid_channel() {
        assert!(MidiChannel::new(16).is_err());
        assert!(MidiChannel::new(255).is_err());
    }

    #[test]
    fn try_from() {
        let ch: MidiChannel = 10u8.try_into().unwrap();
        assert_eq!(ch.value(), 10);
    }
}
