use crate::kernel::MidiGroup::MidiGroup;
use crate::kernel::MidiChannel::MidiChannel;
use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum MidiEventKind {
    NoteOn,
    NoteOff,
    ControlChange,
    PitchBend,
}

#[derive(Debug, Clone, Copy)]
pub struct MidiEvent {
    pub group: MidiGroup,
    pub channel: MidiChannel,
    pub note_id: NoteId,
    pub kind: MidiEventKind,
    pub note_number: NoteNumber,
    pub velocity: Velocity,
    pub value: f64,
}

impl MidiEvent {
    pub fn note_on(
        group: MidiGroup,
        channel: MidiChannel,
        note_id: NoteId,
        note_number: NoteNumber,
        velocity: Velocity,
    ) -> Self {
        Self {
            group,
            channel,
            note_id,
            kind: MidiEventKind::NoteOn,
            note_number,
            velocity,
            value: 0.0,
        }
    }

    pub fn note_off(
        group: MidiGroup,
        channel: MidiChannel,
        note_id: NoteId,
        note_number: NoteNumber,
    ) -> Self {
        Self {
            group,
            channel,
            note_id,
            kind: MidiEventKind::NoteOff,
            note_number,
            velocity: Velocity::new(0.0).unwrap(),
            value: 0.0,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_group() -> MidiGroup {
        MidiGroup::new(0).unwrap()
    }

    fn test_channel() -> MidiChannel {
        MidiChannel::new(0).unwrap()
    }

    fn test_note_number() -> NoteNumber {
        NoteNumber::new(60).unwrap()
    }

    #[test]
    fn note_on_event() {
        let event = MidiEvent::note_on(
            test_group(),
            test_channel(),
            NoteId::new(1),
            test_note_number(),
            Velocity::new(0.8).unwrap(),
        );
        assert_eq!(event.kind, MidiEventKind::NoteOn);
        assert_eq!(event.note_number.value(), 60);
        assert!((event.velocity.value() - 0.8).abs() < f64::EPSILON);
    }

    #[test]
    fn note_off_event() {
        let event = MidiEvent::note_off(
            test_group(),
            test_channel(),
            NoteId::new(1),
            test_note_number(),
        );
        assert_eq!(event.kind, MidiEventKind::NoteOff);
        assert!((event.velocity.value()).abs() < f64::EPSILON);
    }
}
