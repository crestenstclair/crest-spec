use crate::kernel::MidiGroup::MidiGroup;
use crate::kernel::MidiChannel::MidiChannel;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct ChannelAddress {
    group: MidiGroup,
    channel: MidiChannel,
}

impl ChannelAddress {
    pub fn new(group: MidiGroup, channel: MidiChannel) -> Self {
        Self { group, channel }
    }

    pub fn group(self) -> MidiGroup {
        self.group
    }

    pub fn channel(self) -> MidiChannel {
        self.channel
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_address() {
        let group = MidiGroup::new(0).unwrap();
        let channel = MidiChannel::new(5).unwrap();
        let addr = ChannelAddress::new(group, channel);
        assert_eq!(addr.group().value(), 0);
        assert_eq!(addr.channel().value(), 5);
    }

    #[test]
    fn equality() {
        let a = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(1).unwrap(),
        );
        let b = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(1).unwrap(),
        );
        assert_eq!(a, b);
    }

    #[test]
    fn inequality_group() {
        let a = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(1).unwrap(),
        );
        let b = ChannelAddress::new(
            MidiGroup::new(1).unwrap(),
            MidiChannel::new(1).unwrap(),
        );
        assert_ne!(a, b);
    }

    #[test]
    fn inequality_channel() {
        let a = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(1).unwrap(),
        );
        let b = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(2).unwrap(),
        );
        assert_ne!(a, b);
    }

    #[test]
    fn hashable() {
        use std::collections::HashSet;
        let mut set = HashSet::new();
        let addr = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(0).unwrap(),
        );
        set.insert(addr);
        assert!(set.contains(&addr));
    }
}
