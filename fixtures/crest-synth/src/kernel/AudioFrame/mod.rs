#[derive(Debug, Clone, Copy, PartialEq)]
pub struct AudioFrame {
    pub left: f32,
    pub right: f32,
}

impl AudioFrame {
    pub fn new(left: f32, right: f32) -> Self {
        Self { left, right }
    }
}

impl Default for AudioFrame {
    fn default() -> Self {
        Self {
            left: 0.0,
            right: 0.0,
        }
    }
}

impl std::ops::Add for AudioFrame {
    type Output = Self;

    fn add(self, rhs: Self) -> Self::Output {
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

impl std::ops::Mul<f32> for AudioFrame {
    type Output = Self;

    fn mul(self, scalar: f32) -> Self::Output {
        Self {
            left: self.left * scalar,
            right: self.right * scalar,
        }
    }
}

impl std::ops::Mul<AudioFrame> for f32 {
    type Output = AudioFrame;

    fn mul(self, frame: AudioFrame) -> Self::Output {
        AudioFrame {
            left: self * frame.left,
            right: self * frame.right,
        }
    }
}

impl std::ops::MulAssign<f32> for AudioFrame {
    fn mul_assign(&mut self, scalar: f32) {
        self.left *= scalar;
        self.right *= scalar;
    }
}
