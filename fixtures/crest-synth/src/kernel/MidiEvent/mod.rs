use crate::kernel::MidiChannel::MidiChannel;
use crate::kernel::MidiGroup::MidiGroup;
use crate::kernel::NoteId::NoteId;
use crate::kernel::NoteNumber::NoteNumber;
use crate::kernel::Velocity::Velocity;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum MidiEventKind {
    NoteOn,
    NoteOff,
    ControlChange,
    PitchBend,
    Aftertouch,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct MidiEvent {
    pub group: MidiGroup,
    pub channel: MidiChannel,
    pub note_id: NoteId,
    pub kind: MidiEventKind,
    pub note_number: NoteNumber,
    pub velocity: Velocity,
    pub value: f64,
}

#[derive(Debug, Clone, PartialEq)]
pub enum MidiEventError {
    InvalidGroup(u8),
    InvalidChannel,
    InvalidNoteNumber(u8),
    InvalidVelocity(f64),
}

impl MidiEvent {
    pub fn new(
        group: MidiGroup,
        channel: MidiChannel,
        note_id: NoteId,
        kind: MidiEventKind,
        note_number: NoteNumber,
        velocity: Velocity,
        value: f64,
    ) -> Self {
        Self {
            group,
            channel,
            note_id,
            kind,
            note_number,
            velocity,
            value,
        }
    }

    pub fn try_new(
        group_raw: u8,
        channel_raw: u8,
        note_id_raw: u32,
        kind: MidiEventKind,
        note_number_raw: u8,
        velocity_raw: f64,
        value: f64,
    ) -> Result<Self, MidiEventError> {
        let group = MidiGroup::new(group_raw).map_err(MidiEventError::InvalidGroup)?;
        let channel = MidiChannel::new(channel_raw).map_err(|_| MidiEventError::InvalidChannel)?;
        let note_id = NoteId::new(note_id_raw);
        let note_number =
            NoteNumber::new(note_number_raw).map_err(|_| MidiEventError::InvalidNoteNumber(note_number_raw))?;
        let velocity = Velocity::new(velocity_raw).map_err(|_| MidiEventError::InvalidVelocity(velocity_raw))?;

        Ok(Self::new(
            group,
            channel,
            note_id,
            kind,
            note_number,
            velocity,
            value,
        ))
    }

    pub fn group(&self) -> MidiGroup {
        self.group
    }

    pub fn channel(&self) -> MidiChannel {
        self.channel
    }

    pub fn note_id(&self) -> NoteId {
        self.note_id
    }

    pub fn kind(&self) -> MidiEventKind {
        self.kind
    }

    pub fn note_number(&self) -> NoteNumber {
        self.note_number
    }

    pub fn velocity(&self) -> Velocity {
        self.velocity
    }

    pub fn value(&self) -> f64 {
        self.value
    }
}
