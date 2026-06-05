use crate::Shell::MidiInput::RawMidiMessage;
use crate::kernel::MidiEvent::MidiEvent;

pub trait MidiNormalizer: Send + Sync {
    fn normalize(&self, raw: RawMidiMessage) -> MidiEvent;
}
