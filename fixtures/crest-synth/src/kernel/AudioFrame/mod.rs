#[derive(Debug, Clone, Copy, PartialEq)]
pub struct AudioFrame {
    pub left: f32,
    pub right: f32,
}

impl AudioFrame {
    pub fn new(left: f32, right: f32) -> Self {
        Self { left, right }
    }

    pub fn silence() -> Self {
        Self { left: 0.0, right: 0.0 }
    }

    pub fn mono(value: f32) -> Self {
        Self { left: value, right: value }
    }
}

impl Default for AudioFrame {
    fn default() -> Self {
        Self::silence()
    }
}

impl std::ops::Add for AudioFrame {
    type Output = Self;

    fn add(self, rhs: Self) -> Self {
        Self {
            left: self.left + rhs.left,
            right: self.right + rhs.right,
        }
    }
}

impl std::ops::AddAssign for AudioFrame {
    fn add_assign(&mut self, rhs: Self) {
        self.left += rhs.left;
        self.right += rhs.right;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn silence() {
        let frame = AudioFrame::silence();
        assert_eq!(frame.left, 0.0);
        assert_eq!(frame.right, 0.0);
    }

    #[test]
    fn mono() {
        let frame = AudioFrame::mono(0.5);
        assert_eq!(frame.left, 0.5);
        assert_eq!(frame.right, 0.5);
    }

    #[test]
    fn add() {
        let a = AudioFrame::new(0.3, 0.4);
        let b = AudioFrame::new(0.1, 0.2);
        let c = a + b;
        assert!((c.left - 0.4).abs() < f32::EPSILON);
        assert!((c.right - 0.6).abs() < f32::EPSILON);
    }

    #[test]
    fn add_assign() {
        let mut a = AudioFrame::new(0.3, 0.4);
        a += AudioFrame::new(0.1, 0.2);
        assert!((a.left - 0.4).abs() < f32::EPSILON);
        assert!((a.right - 0.6).abs() < f32::EPSILON);
    }

    #[test]
    fn default_is_silence() {
        let frame = AudioFrame::default();
        assert_eq!(frame, AudioFrame::silence());
    }
}
