use crate::kernel::AudioFrame::AudioFrame;
use crate::kernel::Amplitude::Amplitude;

pub struct PatchOutput<'a> {
    pub buffer: &'a [AudioFrame],
    pub gain: Amplitude,
    pub pan: f64,
}

pub struct PatchMixer;

impl PatchMixer {
    pub fn new() -> Self {
        Self
    }

    pub fn mix(patches: &[PatchOutput], output: &mut [AudioFrame]) {
        for frame in output.iter_mut() {
            *frame = AudioFrame::silence();
        }

        for patch in patches {
            let gain = patch.gain.value() as f32;
            let left_gain = ((1.0 - patch.pan) * 0.5) as f32 * gain;
            let right_gain = ((1.0 + patch.pan) * 0.5) as f32 * gain;

            for (i, src) in patch.buffer.iter().enumerate() {
                if i < output.len() {
                    output[i].left += src.left * left_gain;
                    output[i].right += src.right * right_gain;
                }
            }
        }
    }
}

impl Default for PatchMixer {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mix_silence_with_no_patches() {
        let mut output = [AudioFrame::silence(); 4];
        PatchMixer::mix(&[], &mut output);
        for frame in &output {
            assert_eq!(frame.left, 0.0);
            assert_eq!(frame.right, 0.0);
        }
    }

    #[test]
    fn mix_single_patch_center_pan() {
        let buf = [AudioFrame::mono(0.8); 4];
        let patches = [PatchOutput {
            buffer: &buf,
            gain: Amplitude::unity(),
            pan: 0.0,
        }];
        let mut output = [AudioFrame::silence(); 4];
        PatchMixer::mix(&patches, &mut output);
        for frame in &output {
            assert!((frame.left - 0.4).abs() < 0.001);
            assert!((frame.right - 0.4).abs() < 0.001);
        }
    }

    #[test]
    fn mix_hard_left_pan() {
        let buf = [AudioFrame::mono(1.0); 4];
        let patches = [PatchOutput {
            buffer: &buf,
            gain: Amplitude::unity(),
            pan: -1.0,
        }];
        let mut output = [AudioFrame::silence(); 4];
        PatchMixer::mix(&patches, &mut output);
        for frame in &output {
            assert!((frame.left - 1.0).abs() < 0.001);
            assert!((frame.right).abs() < 0.001);
        }
    }

    #[test]
    fn mix_hard_right_pan() {
        let buf = [AudioFrame::mono(1.0); 4];
        let patches = [PatchOutput {
            buffer: &buf,
            gain: Amplitude::unity(),
            pan: 1.0,
        }];
        let mut output = [AudioFrame::silence(); 4];
        PatchMixer::mix(&patches, &mut output);
        for frame in &output {
            assert!((frame.left).abs() < 0.001);
            assert!((frame.right - 1.0).abs() < 0.001);
        }
    }

    #[test]
    fn mix_with_gain() {
        let buf = [AudioFrame::mono(1.0); 4];
        let patches = [PatchOutput {
            buffer: &buf,
            gain: Amplitude::new(0.5).unwrap(),
            pan: 0.0,
        }];
        let mut output = [AudioFrame::silence(); 4];
        PatchMixer::mix(&patches, &mut output);
        for frame in &output {
            assert!((frame.left - 0.25).abs() < 0.001);
            assert!((frame.right - 0.25).abs() < 0.001);
        }
    }

    #[test]
    fn mix_multiple_patches_sum() {
        let buf1 = [AudioFrame::mono(0.5); 4];
        let buf2 = [AudioFrame::mono(0.3); 4];
        let patches = [
            PatchOutput {
                buffer: &buf1,
                gain: Amplitude::unity(),
                pan: 0.0,
            },
            PatchOutput {
                buffer: &buf2,
                gain: Amplitude::unity(),
                pan: 0.0,
            },
        ];
        let mut output = [AudioFrame::silence(); 4];
        PatchMixer::mix(&patches, &mut output);
        for frame in &output {
            assert!((frame.left - 0.4).abs() < 0.01);
        }
    }
}
