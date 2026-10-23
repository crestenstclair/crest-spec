#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum ModDestinationType {
    Pitch,
    FilterCutoff,
    FilterResonance,
    Gain,
    Pan,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn variants_are_distinct() {
        let variants = [
            ModDestinationType::Pitch,
            ModDestinationType::FilterCutoff,
            ModDestinationType::FilterResonance,
            ModDestinationType::Gain,
            ModDestinationType::Pan,
        ];

        for i in 0..variants.len() {
            for j in 0..variants.len() {
                if i == j {
                    assert_eq!(variants[i], variants[j]);
                } else {
                    assert_ne!(variants[i], variants[j]);
                }
            }
        }
    }

    #[test]
    fn debug_formatting() {
        assert_eq!(format!("{:?}", ModDestinationType::Pitch), "Pitch");
        assert_eq!(format!("{:?}", ModDestinationType::FilterCutoff), "FilterCutoff");
        assert_eq!(format!("{:?}", ModDestinationType::FilterResonance), "FilterResonance");
        assert_eq!(format!("{:?}", ModDestinationType::Gain), "Gain");
        assert_eq!(format!("{:?}", ModDestinationType::Pan), "Pan");
    }

    #[test]
    fn hash_consistency() {
        use std::collections::HashSet;
        let mut set = HashSet::new();
        set.insert(ModDestinationType::Pitch);
        set.insert(ModDestinationType::FilterCutoff);
        set.insert(ModDestinationType::FilterResonance);
        set.insert(ModDestinationType::Gain);
        set.insert(ModDestinationType::Pan);
        assert_eq!(set.len(), 5);
    }
}
