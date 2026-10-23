use crate::kernel::ChannelAddress::ChannelAddress;
use crate::Patch::MpeZone::MpeZone;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ChannelSubscription {
    address: ChannelAddress,
    mpe_zone: Option<MpeZone>,
}

impl ChannelSubscription {
    pub fn new(address: ChannelAddress, mpe_zone: Option<MpeZone>) -> Self {
        Self { address, mpe_zone }
    }

    pub fn single(address: ChannelAddress) -> Self {
        Self {
            address,
            mpe_zone: None,
        }
    }

    pub fn address(&self) -> ChannelAddress {
        self.address
    }

    pub fn mpe_zone(&self) -> Option<MpeZone> {
        self.mpe_zone
    }

    pub fn is_mpe(&self) -> bool {
        self.mpe_zone.is_some()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::kernel::MidiGroup::MidiGroup;
    use crate::kernel::MidiChannel::MidiChannel;

    fn addr(group: u8, channel: u8) -> ChannelAddress {
        ChannelAddress::new(
            MidiGroup::new(group).unwrap(),
            MidiChannel::new(channel).unwrap(),
        )
    }

    fn ch(v: u8) -> MidiChannel {
        MidiChannel::new(v).unwrap()
    }

    #[test]
    fn single_channel_subscription() {
        let sub = ChannelSubscription::single(addr(0, 5));
        assert_eq!(sub.address().channel().value(), 5);
        assert!(!sub.is_mpe());
        assert!(sub.mpe_zone().is_none());
    }

    #[test]
    fn mpe_subscription() {
        let zone = MpeZone::new(ch(0), ch(1), ch(7)).unwrap();
        let sub = ChannelSubscription::new(addr(0, 0), Some(zone));
        assert!(sub.is_mpe());
        assert!(sub.mpe_zone().is_some());
    }

    #[test]
    fn equality() {
        let a = ChannelSubscription::single(addr(0, 1));
        let b = ChannelSubscription::single(addr(0, 1));
        assert_eq!(a, b);
    }

    #[test]
    fn inequality() {
        let a = ChannelSubscription::single(addr(0, 1));
        let b = ChannelSubscription::single(addr(0, 2));
        assert_ne!(a, b);
    }
}
