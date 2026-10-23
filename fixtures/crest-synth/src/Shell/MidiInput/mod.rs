#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct MidiPortId(pub String);

#[derive(Debug, Clone)]
pub struct MidiPortInfo {
    pub id: MidiPortId,
    pub name: String,
}

#[derive(Debug, Clone)]
pub struct RawMidiMessage {
    pub data: [u8; 3],
    pub len: usize,
}

pub trait MidiConnection {
    fn is_connected(&self) -> bool;
    fn disconnect(&mut self);
}

pub trait MidiInput {
    type Connection: MidiConnection;
    type Error: std::fmt::Debug;

    fn list_ports(&self) -> Result<Vec<MidiPortInfo>, Self::Error>;
    fn connect(&mut self, port: &MidiPortId) -> Result<Self::Connection, Self::Error>;
    fn next_event(&mut self) -> Option<RawMidiMessage>;
}

#[cfg(test)]
mod tests {
    use super::*;

    struct MockConnection {
        connected: bool,
    }

    impl MidiConnection for MockConnection {
        fn is_connected(&self) -> bool {
            self.connected
        }

        fn disconnect(&mut self) {
            self.connected = false;
        }
    }

    struct MockMidiInput {
        events: Vec<RawMidiMessage>,
    }

    impl MockMidiInput {
        fn new() -> Self {
            Self { events: Vec::new() }
        }

        fn push_event(&mut self, data: [u8; 3], len: usize) {
            self.events.push(RawMidiMessage { data, len });
        }
    }

    impl MidiInput for MockMidiInput {
        type Connection = MockConnection;
        type Error = &'static str;

        fn list_ports(&self) -> Result<Vec<MidiPortInfo>, Self::Error> {
            Ok(vec![MidiPortInfo {
                id: MidiPortId("test-port".to_string()),
                name: "Test Port".to_string(),
            }])
        }

        fn connect(&mut self, _port: &MidiPortId) -> Result<Self::Connection, Self::Error> {
            Ok(MockConnection { connected: true })
        }

        fn next_event(&mut self) -> Option<RawMidiMessage> {
            self.events.pop()
        }
    }

    #[test]
    fn list_ports() {
        let input = MockMidiInput::new();
        let ports = input.list_ports().unwrap();
        assert_eq!(ports.len(), 1);
        assert_eq!(ports[0].name, "Test Port");
    }

    #[test]
    fn connect() {
        let mut input = MockMidiInput::new();
        let conn = input.connect(&MidiPortId("test".to_string())).unwrap();
        assert!(conn.is_connected());
    }

    #[test]
    fn next_event() {
        let mut input = MockMidiInput::new();
        assert!(input.next_event().is_none());

        input.push_event([0x90, 60, 100], 3);
        let event = input.next_event().unwrap();
        assert_eq!(event.data[0], 0x90);
    }
}
