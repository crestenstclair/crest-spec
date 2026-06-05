#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct MidiPortId(pub usize);

#[derive(Debug, Clone)]
pub struct MidiPortInfo {
    pub id: MidiPortId,
    pub name: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct MidiConnection {
    pub id: MidiPortId,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct RawMidiMessage {
    pub data: [u8; 3],
    pub len: u8,
}

pub trait MidiInput {
    fn list_ports(&self) -> Vec<MidiPortInfo>;
    fn connect(&mut self, port_id: MidiPortId) -> MidiConnection;
    fn next_event(&mut self) -> Option<RawMidiMessage>;
}
