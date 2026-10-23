use crate::Modulation::LfoConfig::LfoConfig;
use crate::Modulation::ModEnvelopeConfig::ModEnvelopeConfig;
use crate::Modulation::ModDestinationType::ModDestinationType;
use crate::Modulation::ModSourceType::ModSourceType;
use crate::Patch::PatchId::PatchId;

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ModRouting {
    pub source: ModSourceType,
    pub destination: ModDestinationType,
    pub depth: f64,
}

pub struct ModMatrix {
    patch_id: PatchId,
    routings: Vec<ModRouting>,
    lfo_configs: Vec<LfoConfig>,
    mod_envelopes: Vec<ModEnvelopeConfig>,
}

impl ModMatrix {
    pub fn new(patch_id: PatchId) -> Self {
        Self {
            patch_id,
            routings: Vec::new(),
            lfo_configs: Vec::new(),
            mod_envelopes: Vec::new(),
        }
    }

    pub fn patch_id(&self) -> PatchId {
        self.patch_id
    }

    pub fn routings(&self) -> &[ModRouting] {
        &self.routings
    }

    pub fn lfo_configs(&self) -> &[LfoConfig] {
        &self.lfo_configs
    }

    pub fn mod_envelopes(&self) -> &[ModEnvelopeConfig] {
        &self.mod_envelopes
    }

    pub fn add_routing(
        &mut self,
        source: ModSourceType,
        destination: ModDestinationType,
        depth: f64,
    ) -> Result<(), &'static str> {
        if !(-1.0..=1.0).contains(&depth) {
            return Err("depth must be in range -1.0..=1.0");
        }
        self.routings.push(ModRouting { source, destination, depth });
        Ok(())
    }

    pub fn remove_routing(&mut self, routing_index: usize) -> Result<(), &'static str> {
        if routing_index >= self.routings.len() {
            return Err("routing_index out of bounds");
        }
        self.routings.remove(routing_index);
        Ok(())
    }

    pub fn update_routing_depth(
        &mut self,
        routing_index: usize,
        depth: f64,
    ) -> Result<(), &'static str> {
        if routing_index >= self.routings.len() {
            return Err("routing_index out of bounds");
        }
        if !(-1.0..=1.0).contains(&depth) {
            return Err("depth must be in range -1.0..=1.0");
        }
        self.routings[routing_index].depth = depth;
        Ok(())
    }

    pub fn configure_lfo(
        &mut self,
        lfo_index: usize,
        config: LfoConfig,
    ) -> Result<(), &'static str> {
        if lfo_index >= self.lfo_configs.len() {
            self.lfo_configs.resize_with(lfo_index + 1, LfoConfig::default);
        }
        self.lfo_configs[lfo_index] = config;
        Ok(())
    }

    pub fn configure_mod_envelope(
        &mut self,
        env_index: usize,
        config: ModEnvelopeConfig,
    ) -> Result<(), &'static str> {
        if env_index >= self.mod_envelopes.len() {
            self.mod_envelopes.resize_with(env_index + 1, ModEnvelopeConfig::default);
        }
        self.mod_envelopes[env_index] = config;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn patch_id() -> PatchId {
        PatchId::new(1)
    }

    fn lfo() -> LfoConfig {
        LfoConfig::default()
    }

    fn env() -> ModEnvelopeConfig {
        ModEnvelopeConfig::default()
    }

    #[test]
    fn new_creates_empty_matrix() {
        let m = ModMatrix::new(patch_id());
        assert_eq!(m.patch_id().value(), 1);
        assert!(m.routings().is_empty());
        assert!(m.lfo_configs().is_empty());
        assert!(m.mod_envelopes().is_empty());
    }

    #[test]
    fn add_routing_accepts_valid_depth() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 0.5).is_ok());
        assert_eq!(m.routings().len(), 1);
        assert_eq!(m.routings()[0].depth, 0.5);
    }

    #[test]
    fn add_routing_accepts_boundary_depths() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.add_routing(ModSourceType::Lfo, ModDestinationType::Pitch, -1.0).is_ok());
        assert!(m.add_routing(ModSourceType::Lfo, ModDestinationType::Pitch, 1.0).is_ok());
    }

    #[test]
    fn add_routing_rejects_out_of_range_depth() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 1.1).is_err());
        assert!(m.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, -1.1).is_err());
        assert!(m.routings().is_empty());
    }

    #[test]
    fn remove_routing_removes_correct_entry() {
        let mut m = ModMatrix::new(patch_id());
        m.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 0.5).unwrap();
        m.add_routing(ModSourceType::Lfo, ModDestinationType::Pitch, -0.3).unwrap();
        assert!(m.remove_routing(0).is_ok());
        assert_eq!(m.routings().len(), 1);
        assert_eq!(m.routings()[0].source, ModSourceType::Lfo);
    }

    #[test]
    fn remove_routing_rejects_out_of_bounds_index() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.remove_routing(0).is_err());
        m.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 0.0).unwrap();
        assert!(m.remove_routing(1).is_err());
    }

    #[test]
    fn update_routing_depth_changes_value() {
        let mut m = ModMatrix::new(patch_id());
        m.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 0.0).unwrap();
        assert!(m.update_routing_depth(0, 0.75).is_ok());
        assert_eq!(m.routings()[0].depth, 0.75);
    }

    #[test]
    fn update_routing_depth_rejects_invalid_depth() {
        let mut m = ModMatrix::new(patch_id());
        m.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 0.0).unwrap();
        assert!(m.update_routing_depth(0, 2.0).is_err());
        assert_eq!(m.routings()[0].depth, 0.0);
    }

    #[test]
    fn update_routing_depth_rejects_out_of_bounds_index() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.update_routing_depth(0, 0.5).is_err());
    }

    #[test]
    fn configure_lfo_at_index_zero() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.configure_lfo(0, lfo()).is_ok());
        assert_eq!(m.lfo_configs().len(), 1);
    }

    #[test]
    fn configure_lfo_grows_vec_for_sparse_index() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.configure_lfo(3, lfo()).is_ok());
        assert_eq!(m.lfo_configs().len(), 4);
    }

    #[test]
    fn configure_mod_envelope_at_index_zero() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.configure_mod_envelope(0, env()).is_ok());
        assert_eq!(m.mod_envelopes().len(), 1);
    }

    #[test]
    fn configure_mod_envelope_grows_vec_for_sparse_index() {
        let mut m = ModMatrix::new(patch_id());
        assert!(m.configure_mod_envelope(2, env()).is_ok());
        assert_eq!(m.mod_envelopes().len(), 3);
    }
}
