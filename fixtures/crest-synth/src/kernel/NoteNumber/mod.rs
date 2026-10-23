#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct NoteNumber(u8);

impl NoteNumber {
    pub fn new(value: u8) -> Result<Self, &'static str> {
        if value > 127 {
            return Err("NoteNumber must be 0-127");
        }
        Ok(Self(value))
    }

    pub fn value(self) -> u8 {
        self.0
    }

    pub fn to_frequency(self) -> f64 {
        440.0 * 2.0_f64.powf((self.0 as f64 - 69.0) / 12.0)
    }
}

impl TryFrom<u8> for NoteNumber {
    type Error = &'static str;

    fn try_from(value: u8) -> Result<Self, Self::Error> {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_note() {
        for i in 0..=127 {
            let note = NoteNumber::new(i).unwrap();
            assert_eq!(note.value(), i);
        }
    }

    #[test]
    fn invalid_note() {
        assert!(NoteNumber::new(128).is_err());
        assert!(NoteNumber::new(255).is_err());
    }

    #[test]
    fn a4_frequency() {
        let a4 = NoteNumber::new(69).unwrap();
        assert!((a4.to_frequency() - 440.0).abs() < 0.001);
    }

    #[test]
    fn middle_c_frequency() {
        let c4 = NoteNumber::new(60).unwrap();
        assert!((c4.to_frequency() - 261.626).abs() < 0.01);
    }
}
