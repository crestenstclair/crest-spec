use crate::kernel::MidiEvent::MidiEvent;
use crate::kernel::ChannelAddress::ChannelAddress;
use crate::Patch::Patch::Patch;

pub struct ChannelDispatcher;

impl ChannelDispatcher {
    pub fn new() -> Self {
        Self
    }

    pub fn address_of(event: &MidiEvent) -> ChannelAddress {
        ChannelAddress::new(event.group, event.channel)
    }

    pub fn find_subscribers<'a>(
        patches: &'a [Patch],
        event: &MidiEvent,
    ) -> Vec<usize> {
        let target = Self::address_of(event);
        patches
            .iter()
            .enumerate()
            .filter(|(_, p)| p.is_active() && p.subscription().address() == target)
            .map(|(i, _)| i)
            .collect()
    }

    pub fn dispatch<F>(
        patches: &[Patch],
        event: &MidiEvent,
        mut handler: F,
    ) where
        F: FnMut(usize, &MidiEvent),
    {
        let subscribers = Self::find_subscribers(patches, event);
        for idx in subscribers {
            handler(idx, event);
        }
    }
}

impl Default for ChannelDispatcher {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::kernel::MidiGroup::MidiGroup;
    use crate::kernel::MidiChannel::MidiChannel;
    use crate::kernel::NoteId::NoteId;
    use crate::kernel::NoteNumber::NoteNumber;
    use crate::kernel::Velocity::Velocity;
    use crate::Patch::PatchId::PatchId;
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

    fn note_on(ch: u8, note: u8) -> MidiEvent {
        MidiEvent::note_on(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(ch).unwrap(),
            NoteId::new(1),
            NoteNumber::new(note).unwrap(),
            Velocity::new(0.8).unwrap(),
        )
    }

    #[test]
    fn dispatches_to_matching_channel() {
        let patches = vec![make_patch(0, 0), make_patch(1, 1)];
        let event = note_on(0, 60);
        let subscribers = ChannelDispatcher::find_subscribers(&patches, &event);
        assert_eq!(subscribers, vec![0]);
    }

    #[test]
    fn dispatches_to_all_matching_patches() {
        let patches = vec![make_patch(0, 0), make_patch(1, 0), make_patch(2, 1)];
        let event = note_on(0, 60);
        let subscribers = ChannelDispatcher::find_subscribers(&patches, &event);
        assert_eq!(subscribers, vec![0, 1]);
    }

    #[test]
    fn skips_inactive_patches() {
        let mut patch = make_patch(0, 0);
        patch.deactivate();
        let patches = vec![patch, make_patch(1, 0)];
        let event = note_on(0, 60);
        let subscribers = ChannelDispatcher::find_subscribers(&patches, &event);
        assert_eq!(subscribers, vec![1]);
    }

    #[test]
    fn no_subscribers_returns_empty() {
        let patches = vec![make_patch(0, 0)];
        let event = note_on(5, 60);
        let subscribers = ChannelDispatcher::find_subscribers(&patches, &event);
        assert!(subscribers.is_empty());
    }

    #[test]
    fn dispatch_calls_handler_for_each_subscriber() {
        let patches = vec![make_patch(0, 0), make_patch(1, 0)];
        let event = note_on(0, 60);
        let mut called = Vec::new();
        ChannelDispatcher::dispatch(&patches, &event, |idx, _| {
            called.push(idx);
        });
        assert_eq!(called, vec![0, 1]);
    }

    #[test]
    fn address_of_extracts_correctly() {
        let event = note_on(3, 60);
        let addr = ChannelDispatcher::address_of(&event);
        assert_eq!(addr.group().value(), 0);
        assert_eq!(addr.channel().value(), 3);
    }
}
