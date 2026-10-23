use crate::RealTime::BoundaryMessage::BoundaryMessage;

#[derive(Debug)]
pub struct Full;

impl std::fmt::Display for Full {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "ring buffer is full")
    }
}

pub trait EventRingBuffer {
    fn push(&mut self, message: BoundaryMessage) -> Result<(), Full>;
    fn pop(&mut self) -> Option<BoundaryMessage>;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::RealTime::BoundaryMessage::BoundaryMessageKind;

    struct MockRingBuffer {
        buffer: Vec<BoundaryMessage>,
        capacity: usize,
    }

    impl MockRingBuffer {
        fn new(capacity: usize) -> Self {
            Self {
                buffer: Vec::new(),
                capacity,
            }
        }
    }

    impl EventRingBuffer for MockRingBuffer {
        fn push(&mut self, message: BoundaryMessage) -> Result<(), Full> {
            if self.buffer.len() >= self.capacity {
                return Err(Full);
            }
            self.buffer.push(message);
            Ok(())
        }

        fn pop(&mut self) -> Option<BoundaryMessage> {
            if self.buffer.is_empty() {
                None
            } else {
                Some(self.buffer.remove(0))
            }
        }
    }

    #[test]
    fn push_and_pop() {
        let mut rb = MockRingBuffer::new(16);
        let msg = BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![60, 100], 1);
        rb.push(msg).unwrap();

        let popped = rb.pop().unwrap();
        assert_eq!(popped.kind, BoundaryMessageKind::NoteOn);
        assert_eq!(popped.sequence_number, 1);
    }

    #[test]
    fn pop_empty_returns_none() {
        let mut rb = MockRingBuffer::new(16);
        assert!(rb.pop().is_none());
    }

    #[test]
    fn push_when_full_returns_error() {
        let mut rb = MockRingBuffer::new(1);
        let msg1 = BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![], 1);
        let msg2 = BoundaryMessage::new(BoundaryMessageKind::NoteOff, vec![], 2);

        rb.push(msg1).unwrap();
        assert!(rb.push(msg2).is_err());
    }

    #[test]
    fn fifo_ordering() {
        let mut rb = MockRingBuffer::new(16);
        let msg1 = BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![], 1);
        let msg2 = BoundaryMessage::new(BoundaryMessageKind::NoteOff, vec![], 2);

        rb.push(msg1).unwrap();
        rb.push(msg2).unwrap();

        assert_eq!(rb.pop().unwrap().sequence_number, 1);
        assert_eq!(rb.pop().unwrap().sequence_number, 2);
    }
}
