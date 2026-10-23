#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct MidiGroup(u8);

impl MidiGroup {
    pub fn new(value: u8) -> Result<Self, &'static str> {
        if value > 15 {
            return Err("MidiGroup must be 0-15");
        }
        Ok(Self(value))
    }

    pub fn value(self) -> u8 {
        self.0
    }
}

impl TryFrom<u8> for MidiGroup {
    type Error = &'static str;

    fn try_from(value: u8) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_group() {
        for i in 0..=15 {
            let group = MidiGroup::new(i).unwrap();
            assert_eq!(group.value(), i);
        }
    }

    #[test]
    fn invalid_group() {
        assert!(MidiGroup::new(16).is_err());
        assert!(MidiGroup::new(255).is_err());
    }

    #[test]
    fn try_from() {
        let group: MidiGroup = 5u8.try_into().unwrap();
        assert_eq!(group.value(), 5);
    }
}
