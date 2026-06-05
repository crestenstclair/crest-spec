// path: tests/Synth/EnvelopeStage/mod.rs

#[cfg(test)]
mod tests {
    use crest_synth::Synth::EnvelopeStage::EnvelopeStage;
    use std::collections::HashSet;

    #[test]
    fn default_is_idle() {
        assert_eq!(EnvelopeStage::default(), EnvelopeStage::Idle);
    }

    #[test]
    fn copy_semantics() {
        let a = EnvelopeStage::Attack;
        let b = a;
        assert_eq!(a, b);
    }

    #[test]
    fn clone_equals_original() {
        let stage = EnvelopeStage::Decay;
        let cloned = stage.clone();
        assert_eq!(stage, cloned);
    }

    #[test]
    fn all_variants_are_distinct() {
        let variants = [
            EnvelopeStage::Idle,
            EnvelopeStage::Attack,
            EnvelopeStage::Decay,
            EnvelopeStage::Sustain,
            EnvelopeStage::Release,
        ];

        for (i, a) in variants.iter().enumerate() {
            for (j, b) in variants.iter().enumerate() {
                if i == j {
                    assert_eq!(a, b);
                } else {
                    assert_ne!(a, b);
                }
            }
        }
    }

    #[test]
    fn hashable_in_set() {
        let mut set = HashSet::new();
        set.insert(EnvelopeStage::Attack);
        set.insert(EnvelopeStage::Attack);
        set.insert(EnvelopeStage::Sustain);

        assert_eq!(set.len(), 2);
        assert!(set.contains(&EnvelopeStage::Attack));
        assert!(set.contains(&EnvelopeStage::Sustain));
        assert!(!set.contains(&EnvelopeStage::Idle));
    }

    #[test]
    fn display_idle() {
        assert_eq!(format!("{}", EnvelopeStage::Idle), "Idle");
    }

    #[test]
    fn display_attack() {
        assert_eq!(format!("{}", EnvelopeStage::Attack), "Attack");
    }

    #[test]
    fn display_decay() {
        assert_eq!(format!("{}", EnvelopeStage::Decay), "Decay");
    }

    #[test]
    fn display_sustain() {
        assert_eq!(format!("{}", EnvelopeStage::Sustain), "Sustain");
    }

    #[test]
    fn display_release() {
        assert_eq!(format!("{}", EnvelopeStage::Release), "Release");
    }

    #[test]
    fn debug_output() {
        let debug = format!("{:?}", EnvelopeStage::Attack);
        assert_eq!(debug, "Attack");
    }
}
