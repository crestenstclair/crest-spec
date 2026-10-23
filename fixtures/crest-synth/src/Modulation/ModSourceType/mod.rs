#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ModSourceType {
    Envelope,
    Lfo,
    Random,
    Macro,
    Velocity,
    KeyTrack,
    PerNoteBendX,
    PerNoteTimbreY,
    PerNotePressureZ,
}

impl ModSourceType {
    pub fn is_per_voice(&self) -> bool {
        matches!(
            self,
            Self::Velocity
                | Self::KeyTrack
                | Self::PerNoteBendX
                | Self::PerNoteTimbreY
                | Self::PerNotePressureZ
        )
    }

    pub fn is_per_patch(&self) -> bool {
        matches!(self, Self::Envelope | Self::Lfo | Self::Random | Self::Macro)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn per_voice_variants_return_true_for_is_per_voice() {
        let per_voice = [
            ModSourceType::Velocity,
            ModSourceType::KeyTrack,
            ModSourceType::PerNoteBendX,
            ModSourceType::PerNoteTimbreY,
            ModSourceType::PerNotePressureZ,
        ];
        for variant in per_voice {
            assert!(variant.is_per_voice(), "{variant:?} should be per-voice");
            assert!(!variant.is_per_patch(), "{variant:?} should not be per-patch");
        }
    }

    #[test]
    fn per_patch_variants_return_true_for_is_per_patch() {
        let per_patch = [
            ModSourceType::Envelope,
            ModSourceType::Lfo,
            ModSourceType::Random,
            ModSourceType::Macro,
        ];
        for variant in per_patch {
            assert!(variant.is_per_patch(), "{variant:?} should be per-patch");
            assert!(!variant.is_per_voice(), "{variant:?} should not be per-voice");
        }
    }

    #[test]
    fn is_per_voice_and_is_per_patch_are_mutually_exclusive_and_exhaustive() {
        let all = [
            ModSourceType::Envelope,
            ModSourceType::Lfo,
            ModSourceType::Random,
            ModSourceType::Macro,
            ModSourceType::Velocity,
            ModSourceType::KeyTrack,
            ModSourceType::PerNoteBendX,
            ModSourceType::PerNoteTimbreY,
            ModSourceType::PerNotePressureZ,
        ];
        for variant in all {
            assert!(
                variant.is_per_voice() ^ variant.is_per_patch(),
                "{variant:?} must be exactly one of per-voice or per-patch"
            );
        }
    }

    #[test]
    fn derives_hash() {
        use std::collections::HashSet;
        let set: HashSet<ModSourceType> = [
            ModSourceType::Envelope,
            ModSourceType::Envelope,
            ModSourceType::Velocity,
        ]
        .into_iter()
        .collect();
        assert_eq!(set.len(), 2);
    }
}
