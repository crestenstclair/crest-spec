use crate::kernel::Amplitude::Amplitude;
use crate::kernel::AudioFrame::AudioFrame;

#[derive(Debug, Clone)]
pub enum GlobalMixerEvent {
    MasterGainChanged { gain: Amplitude },
}

#[derive(Debug)]
pub struct GlobalMixer {
    master_gain: Amplitude,
}

impl GlobalMixer {
    pub fn new(master_gain: Amplitude) -> Self {
        Self { master_gain }
    }

    pub fn master_gain(&self) -> Amplitude {
        self.master_gain
    }

    pub fn set_master_gain(&mut self, gain: Amplitude) -> GlobalMixerEvent {
        self.master_gain = gain;
        GlobalMixerEvent::MasterGainChanged { gain }
    }

    pub fn apply(&self, frame: AudioFrame) -> AudioFrame {
        let g = self.master_gain.value() as f32;
        AudioFrame::new(frame.left * g, frame.right * g)
    }
}

impl Default for GlobalMixer {
    fn default() -> Self {
        Self {
            master_gain: Amplitude::unity(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_is_unity() {
        let mixer = GlobalMixer::default();
        assert!((mixer.master_gain().value() - 1.0).abs() < f64::EPSILON);
    }

    #[test]
    fn set_master_gain() {
        let mut mixer = GlobalMixer::default();
        let gain = Amplitude::new(0.5).unwrap();
        let event = mixer.set_master_gain(gain);
        assert!((mixer.master_gain().value() - 0.5).abs() < f64::EPSILON);
        assert!(matches!(event, GlobalMixerEvent::MasterGainChanged { .. }));
    }

    #[test]
    fn apply_scales_frame() {
        let mixer = GlobalMixer::new(Amplitude::new(0.5).unwrap());
        let frame = AudioFrame::new(0.8, 0.6);
        let result = mixer.apply(frame);
        assert!((result.left - 0.4).abs() < 0.001);
        assert!((result.right - 0.3).abs() < 0.001);
    }

    #[test]
    fn apply_unity_passthrough() {
        let mixer = GlobalMixer::default();
        let frame = AudioFrame::new(0.8, 0.6);
        let result = mixer.apply(frame);
        assert!((result.left - 0.8).abs() < f32::EPSILON);
        assert!((result.right - 0.6).abs() < f32::EPSILON);
    }

    #[test]
    fn apply_silence() {
        let mixer = GlobalMixer::new(Amplitude::silence());
        let frame = AudioFrame::new(0.8, 0.6);
        let result = mixer.apply(frame);
        assert!((result.left).abs() < f32::EPSILON);
        assert!((result.right).abs() < f32::EPSILON);
    }
}
