use crate::kernel::MidiChannel::MidiChannel;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct MpeZone {
    manager_channel: MidiChannel,
    member_channel_start: MidiChannel,
    member_channel_end: MidiChannel,
}

impl MpeZone {
    pub fn new(
        manager_channel: MidiChannel,
        member_channel_start: MidiChannel,
        member_channel_end: MidiChannel,
    ) -> Result<Self, &'static str> {
        if member_channel_start.value() >= member_channel_end.value() {
            return Err("memberChannelStart must be less than memberChannelEnd");
        }
        if manager_channel.value() >= member_channel_start.value()
            && manager_channel.value() <= member_channel_end.value()
        {
            return Err("manager channel must not overlap member channels");
        }
        Ok(Self {
            manager_channel,
            member_channel_start,
            member_channel_end,
        })
    }

    pub fn manager_channel(self) -> MidiChannel {
        self.manager_channel
    }

    pub fn member_channel_start(self) -> MidiChannel {
        self.member_channel_start
    }

    pub fn member_channel_end(self) -> MidiChannel {
        self.member_channel_end
    }

    pub fn contains_channel(self, channel: MidiChannel) -> bool {
        channel.value() >= self.member_channel_start.value()
            && channel.value() <= self.member_channel_end.value()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ch(v: u8) -> MidiChannel {
        MidiChannel::new(v).unwrap()
    }

    #[test]
    fn valid_lower_zone() {
        let zone = MpeZone::new(ch(0), ch(1), ch(7)).unwrap();
        assert_eq!(zone.manager_channel().value(), 0);
        assert_eq!(zone.member_channel_start().value(), 1);
        assert_eq!(zone.member_channel_end().value(), 7);
    }

    #[test]
    fn valid_upper_zone() {
        let zone = MpeZone::new(ch(15), ch(8), ch(14)).unwrap();
        assert_eq!(zone.manager_channel().value(), 15);
    }

    #[test]
    fn rejects_start_equal_to_end() {
        assert!(MpeZone::new(ch(0), ch(5), ch(5)).is_err());
    }

    #[test]
    fn rejects_start_greater_than_end() {
        assert!(MpeZone::new(ch(0), ch(7), ch(3)).is_err());
    }

    #[test]
    fn rejects_overlapping_manager() {
        assert!(MpeZone::new(ch(3), ch(1), ch(7)).is_err());
    }

    #[test]
    fn contains_member_channel() {
        let zone = MpeZone::new(ch(0), ch(1), ch(7)).unwrap();
        assert!(zone.contains_channel(ch(1)));
        assert!(zone.contains_channel(ch(4)));
        assert!(zone.contains_channel(ch(7)));
        assert!(!zone.contains_channel(ch(0)));
        assert!(!zone.contains_channel(ch(8)));
    }
}
