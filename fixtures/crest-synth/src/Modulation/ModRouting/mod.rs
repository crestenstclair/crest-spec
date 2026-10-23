use crate::Modulation::ModSourceType::ModSourceType;
use crate::Modulation::ModDestinationType::ModDestinationType;

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ModRouting {
    source: ModSourceType,
    destination: ModDestinationType,
    depth: f64,
}

impl ModRouting {
    pub fn new(
        source: ModSourceType,
        destination: ModDestinationType,
        depth: f64,
    ) -> Result<Self, &'static str> {
        if !(-1.0..=1.0).contains(&depth) {
            return Err("depth must be in range -1.0..=1.0");
        }
        Ok(Self { source, destination, depth })
    }

    pub fn source(&self) -> ModSourceType {
        self.source
    }

    pub fn destination(&self) -> ModDestinationType {
        self.destination
    }

    pub fn depth(&self) -> f64 {
        self.depth
    }

    pub fn set_depth(&mut self, depth: f64) -> Result<(), &'static str> {
        if !(-1.0..=1.0).contains(&depth) {
            return Err("depth must be in range -1.0..=1.0");
        }
        self.depth = depth;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_routing(depth: f64) -> Result<ModRouting, &'static str> {
        ModRouting::new(
            ModSourceType::Velocity,
            ModDestinationType::FilterCutoff,
            depth,
        )
    }

    #[test]
    fn new_accepts_valid_depths() {
        assert!(make_routing(0.0).is_ok());
        assert!(make_routing(1.0).is_ok());
        assert!(make_routing(-1.0).is_ok());
        assert!(make_routing(0.5).is_ok());
    }

    #[test]
    fn new_rejects_out_of_range_depths() {
        assert_eq!(make_routing(1.0001), Err("depth must be in range -1.0..=1.0"));
        assert_eq!(make_routing(-1.0001), Err("depth must be in range -1.0..=1.0"));
    }

    #[test]
    fn getters_return_constructed_values() {
        let r = ModRouting::new(ModSourceType::Lfo, ModDestinationType::Pitch, 0.5).unwrap();
        assert_eq!(r.source(), ModSourceType::Lfo);
        assert_eq!(r.destination(), ModDestinationType::Pitch);
        assert_eq!(r.depth(), 0.5);
    }

    #[test]
    fn set_depth_updates_value_within_range() {
        let mut r = make_routing(0.0).unwrap();
        assert!(r.set_depth(0.75).is_ok());
        assert_eq!(r.depth(), 0.75);
    }

    #[test]
    fn set_depth_rejects_out_of_range() {
        let mut r = make_routing(0.0).unwrap();
        assert!(r.set_depth(2.0).is_err());
        assert_eq!(r.depth(), 0.0);
    }

    #[test]
    fn copy_produces_independent_value() {
        let original = make_routing(0.3).unwrap();
        let mut copy = original;
        copy.set_depth(0.9).unwrap();
        assert_eq!(original.depth(), 0.3);
        assert_eq!(copy.depth(), 0.9);
    }
}
