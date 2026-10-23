#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct PatchId(u32);

impl PatchId {
    pub fn new(value: u32) -> Self {
        Self(value)
    }

    pub fn value(self) -> u32 {
        self.0
    }
}

impl From<u32> for PatchId {
    fn from(value: u32) -> Self {
        Self::new(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_patch_id() {
        let id = PatchId::new(42);
        assert_eq!(id.value(), 42);
    }

    #[test]
    fn from_u32() {
        let id: PatchId = 100u32.into();
        assert_eq!(id.value(), 100);
    }

    #[test]
    fn equality() {
        assert_eq!(PatchId::new(1), PatchId::new(1));
        assert_ne!(PatchId::new(1), PatchId::new(2));
    }

    #[test]
    fn hashable() {
        use std::collections::HashSet;
        let mut set = HashSet::new();
        set.insert(PatchId::new(1));
        assert!(set.contains(&PatchId::new(1)));
    }
}
