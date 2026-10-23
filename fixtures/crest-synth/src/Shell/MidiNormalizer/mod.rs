use crate::kernel::MidiEvent::MidiEvent;
use crate::Shell::MidiInput::RawMidiMessage;

pub trait MidiNormalizer {
    fn normalize(&self, raw: &RawMidiMessage) -> Option<MidiEvent>;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::kernel::MidiGroup::MidiGroup;
    use crate::kernel::MidiChannel::MidiChannel;
    use crate::kernel::NoteId::NoteId;
    use crate::kernel::NoteNumber::NoteNumber;
    use crate::kernel::Velocity::Velocity;
    use crate::kernel::MidiEvent::MidiEventKind;

    struct StubNormalizer;

    impl MidiNormalizer for StubNormalizer {
        fn normalize(&self, raw: &RawMidiMessage) -> Option<MidiEvent> {
            if raw.len < 3 {
                return None;
            }
            let status = raw.data[0] & 0xF0;
            let channel = raw.data[0] & 0x0F;
            match status {
                0x90 => Some(MidiEvent::note_on(
                    MidiGroup::new(0).unwrap(),
                    MidiChannel::new(channel).unwrap(),
                    NoteId::new(raw.data[1] as u32),
                    NoteNumber::new(raw.data[1]).unwrap(),
                    Velocity::from_midi1(raw.data[2]),
                )),
                0x80 => Some(MidiEvent::note_off(
                    MidiGroup::new(0).unwrap(),
                    MidiChannel::new(channel).unwrap(),
                    NoteId::new(raw.data[1] as u32),
                    NoteNumber::new(raw.data[1]).unwrap(),
                )),
                _ => None,
            }
        }
    }

    #[test]
    fn normalize_note_on() {
        let normalizer = StubNormalizer;
        let raw = RawMidiMessage { data: [0x90, 60, 100], len: 3 };
        let event = normalizer.normalize(&raw).unwrap();
        assert_eq!(event.kind, MidiEventKind::NoteOn);
        assert_eq!(event.note_number.value(), 60);
    }

    #[test]
    fn normalize_note_off() {
        let normalizer = StubNormalizer;
        let raw = RawMidiMessage { data: [0x80, 60, 0], len: 3 };
        let event = normalizer.normalize(&raw).unwrap();
        assert_eq!(event.kind, MidiEventKind::NoteOff);
    }

    #[test]
    fn normalize_short_message_returns_none() {
        let normalizer = StubNormalizer;
        let raw = RawMidiMessage { data: [0x90, 0, 0], len: 2 };
        assert!(normalizer.normalize(&raw).is_none());
    }
}
