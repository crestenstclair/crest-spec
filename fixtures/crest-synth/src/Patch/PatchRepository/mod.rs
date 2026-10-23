use crate::kernel::ChannelAddress::ChannelAddress;
use crate::Patch::PatchId::PatchId;
use crate::Patch::Patch::Patch;

pub trait PatchRepository {
    fn find_by_id(&self, id: PatchId) -> Option<&Patch>;
    fn find_by_channel(&self, address: ChannelAddress) -> Vec<&Patch>;
    fn save(&mut self, patch: Patch);
    fn list_all(&self) -> Vec<&Patch>;
}

pub struct InMemoryPatchRepository {
    patches: Vec<Patch>,
}

impl InMemoryPatchRepository {
    pub fn new() -> Self {
        Self {
            patches: Vec::new(),
        }
    }
}

impl Default for InMemoryPatchRepository {
    fn default() -> Self {
        Self::new()
    }
}

impl PatchRepository for InMemoryPatchRepository {
    fn find_by_id(&self, id: PatchId) -> Option<&Patch> {
        self.patches.iter().find(|p| p.id() == id)
    }

    fn find_by_channel(&self, address: ChannelAddress) -> Vec<&Patch> {
        self.patches
            .iter()
            .filter(|p| p.subscription().address() == address)
            .collect()
    }

    fn save(&mut self, patch: Patch) {
        if let Some(existing) = self.patches.iter_mut().find(|p| p.id() == patch.id()) {
            *existing = patch;
        } else {
            self.patches.push(patch);
        }
    }

    fn list_all(&self) -> Vec<&Patch> {
        self.patches.iter().collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::kernel::MidiGroup::MidiGroup;
    use crate::kernel::MidiChannel::MidiChannel;
    use crate::Patch::ChannelSubscription::ChannelSubscription;
    use crate::Patch::Patch::EngineType;

    fn addr(ch: u8) -> ChannelAddress {
        ChannelAddress::new(MidiGroup::new(0).unwrap(), MidiChannel::new(ch).unwrap())
    }

    fn make_patch(id: u32, ch: u8) -> Patch {
        Patch::new(
            PatchId::new(id),
            format!("Patch {}", id),
            EngineType::Subtractive,
            ChannelSubscription::single(addr(ch)),
        )
    }

    #[test]
    fn save_and_find_by_id() {
        let mut repo = InMemoryPatchRepository::new();
        repo.save(make_patch(1, 0));
        let found = repo.find_by_id(PatchId::new(1));
        assert!(found.is_some());
        assert_eq!(found.unwrap().name(), "Patch 1");
    }

    #[test]
    fn find_by_id_not_found() {
        let repo = InMemoryPatchRepository::new();
        assert!(repo.find_by_id(PatchId::new(99)).is_none());
    }

    #[test]
    fn find_by_channel() {
        let mut repo = InMemoryPatchRepository::new();
        repo.save(make_patch(1, 0));
        repo.save(make_patch(2, 0));
        repo.save(make_patch(3, 1));

        let found = repo.find_by_channel(addr(0));
        assert_eq!(found.len(), 2);
    }

    #[test]
    fn find_by_channel_empty() {
        let mut repo = InMemoryPatchRepository::new();
        repo.save(make_patch(1, 0));
        let found = repo.find_by_channel(addr(5));
        assert!(found.is_empty());
    }

    #[test]
    fn save_updates_existing() {
        let mut repo = InMemoryPatchRepository::new();
        repo.save(make_patch(1, 0));
        repo.save(Patch::new(
            PatchId::new(1),
            "Updated".to_string(),
            EngineType::Fm,
            ChannelSubscription::single(addr(5)),
        ));
        let all = repo.list_all();
        assert_eq!(all.len(), 1);
        assert_eq!(all[0].name(), "Updated");
    }

    #[test]
    fn list_all() {
        let mut repo = InMemoryPatchRepository::new();
        repo.save(make_patch(1, 0));
        repo.save(make_patch(2, 1));
        repo.save(make_patch(3, 2));
        assert_eq!(repo.list_all().len(), 3);
    }

    #[test]
    fn list_all_empty() {
        let repo = InMemoryPatchRepository::new();
        assert!(repo.list_all().is_empty());
    }
}
