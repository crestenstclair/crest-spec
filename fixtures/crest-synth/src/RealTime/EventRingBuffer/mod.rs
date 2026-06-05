use crate::RealTime::BoundaryMessage::BoundaryMessage;

/// Error returned when the ring buffer is full and cannot accept another message.
///
/// Wraps the rejected `BoundaryMessage` so the caller can retry or drop it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Full(pub BoundaryMessage);

impl core::fmt::Display for Full {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        write!(
            f,
            "ring buffer is full; message seq={} dropped",
            self.0.sequence_number()
        )
    }
}

/// Producer side of the event ring buffer.
///
/// Used by the MIDI/UI thread to enqueue `BoundaryMessage`s destined for the
/// audio thread.  Implementations must be lock-free so that the producer never
/// blocks the audio thread's consumer.
pub trait EventRingBufferProducer {
    /// Attempts to push a message into the ring buffer.
    ///
    /// Returns `Err(Full)` if the buffer has no remaining capacity, giving
    /// the caller back the rejected message.
    fn push(&self, msg: BoundaryMessage) -> Result<(), Full>;
}

/// Consumer side of the event ring buffer.
///
/// Used by the audio thread to drain `BoundaryMessage`s.  Implementations
/// must be lock-free and allocation-free so they are safe to call from the
/// real-time audio callback.
pub trait EventRingBufferConsumer {
    /// Pops the next message from the ring buffer, if one is available.
    ///
    /// Returns `None` when the buffer is empty.
    fn pop(&self) -> Option<BoundaryMessage>;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::RealTime::BoundaryMessage::{BoundaryMessage, BoundaryMessageKind};
    use std::cell::RefCell;
    use std::collections::VecDeque;

    // ── Mock implementation ────────────────────────────────────────

    /// A mock SPSC ring buffer backed by a `VecDeque` behind `RefCell`.
    ///
    /// Interior mutability via `RefCell` keeps the trait signatures
    /// (`&self`) satisfied while allowing tests to push and pop without
    /// `&mut self`.  NOT suitable for real multi-threaded use.
    struct MockRingBuffer {
        queue: RefCell<VecDeque<BoundaryMessage>>,
        capacity: usize,
    }

    impl MockRingBuffer {
        fn new(capacity: usize) -> Self {
            Self {
                queue: RefCell::new(VecDeque::with_capacity(capacity)),
                capacity,
            }
        }

        fn len(&self) -> usize {
            self.queue.borrow().len()
        }

        fn is_empty(&self) -> bool {
            self.queue.borrow().is_empty()
        }
    }

    impl EventRingBufferProducer for MockRingBuffer {
        fn push(&self, msg: BoundaryMessage) -> Result<(), Full> {
            let mut q = self.queue.borrow_mut();
            if q.len() >= self.capacity {
                return Err(Full(msg));
            }
            q.push_back(msg);
            Ok(())
        }
    }

    impl EventRingBufferConsumer for MockRingBuffer {
        fn pop(&self) -> Option<BoundaryMessage> {
            self.queue.borrow_mut().pop_front()
        }
    }

    // ── Helpers ────────────────────────────────────────────────────

    fn note_on(seq: u64) -> BoundaryMessage {
        BoundaryMessage::new(BoundaryMessageKind::NoteOn, vec![60, 127], seq)
            .expect("valid note-on")
    }

    fn note_off(seq: u64) -> BoundaryMessage {
        BoundaryMessage::new(BoundaryMessageKind::NoteOff, vec![60, 0], seq)
            .expect("valid note-off")
    }

    // ── Push / Pop ─────────────────────────────────────────────────

    #[test]
    fn push_then_pop_returns_same_message() {
        let buf = MockRingBuffer::new(4);
        let msg = note_on(1);

        buf.push(msg.clone()).expect("buffer not full");
        let popped = buf.pop().expect("buffer not empty");

        assert_eq!(popped, msg);
    }

    #[test]
    fn pop_on_empty_returns_none() {
        let buf = MockRingBuffer::new(4);
        assert_eq!(buf.pop(), None);
    }

    #[test]
    fn fifo_ordering_preserved() {
        let buf = MockRingBuffer::new(8);
        let m1 = note_on(1);
        let m2 = note_off(2);
        let m3 = note_on(3);

        buf.push(m1.clone()).unwrap();
        buf.push(m2.clone()).unwrap();
        buf.push(m3.clone()).unwrap();

        assert_eq!(buf.pop(), Some(m1));
        assert_eq!(buf.pop(), Some(m2));
        assert_eq!(buf.pop(), Some(m3));
        assert_eq!(buf.pop(), None);
    }

    // ── Capacity / Full ────────────────────────────────────────────

    #[test]
    fn push_returns_full_when_at_capacity() {
        let buf = MockRingBuffer::new(2);

        buf.push(note_on(1)).unwrap();
        buf.push(note_on(2)).unwrap();

        let overflow = note_on(3);
        let err = buf.push(overflow.clone());

        assert_eq!(err, Err(Full(overflow)));
    }

    #[test]
    fn full_error_returns_rejected_message() {
        let buf = MockRingBuffer::new(1);
        buf.push(note_on(1)).unwrap();

        let rejected = note_off(2);
        let err = buf.push(rejected.clone()).unwrap_err();

        assert_eq!(err.0, rejected);
    }

    #[test]
    fn push_succeeds_after_pop_frees_slot() {
        let buf = MockRingBuffer::new(1);

        buf.push(note_on(1)).unwrap();
        assert!(buf.push(note_on(2)).is_err());

        buf.pop();
        buf.push(note_on(3)).expect("slot freed; push should succeed");

        assert_eq!(buf.pop().unwrap().sequence_number(), 3);
    }

    // ── Zero-capacity edge case ────────────────────────────────────

    #[test]
    fn zero_capacity_buffer_always_full() {
        let buf = MockRingBuffer::new(0);

        let msg = note_on(1);
        assert!(buf.push(msg).is_err());
        assert_eq!(buf.pop(), None);
    }

    // ── Len / Empty helpers ────────────────────────────────────────

    #[test]
    fn len_tracks_occupancy() {
        let buf = MockRingBuffer::new(4);

        assert!(buf.is_empty());
        assert_eq!(buf.len(), 0);

        buf.push(note_on(1)).unwrap();
        assert_eq!(buf.len(), 1);

        buf.push(note_on(2)).unwrap();
        assert_eq!(buf.len(), 2);

        buf.pop();
        assert_eq!(buf.len(), 1);

        buf.pop();
        assert!(buf.is_empty());
    }

    // ── Full error Display ─────────────────────────────────────────

    #[test]
    fn full_display_contains_sequence_number() {
        let msg = note_on(42);
        let err = Full(msg);
        let text = format!("{err}");
        assert!(text.contains("42"), "expected seq in display, got: {text}");
        assert!(text.contains("full"), "expected 'full' in display, got: {text}");
    }

    // ── Trait object safety ────────────────────────────────────────

    #[test]
    fn producer_is_object_safe() {
        let buf = MockRingBuffer::new(4);
        let producer: &dyn EventRingBufferProducer = &buf;
        producer.push(note_on(1)).unwrap();
    }

    #[test]
    fn consumer_is_object_safe() {
        let buf = MockRingBuffer::new(4);
        buf.push(note_on(1)).unwrap();

        let consumer: &dyn EventRingBufferConsumer = &buf;
        assert!(consumer.pop().is_some());
    }

    // ── Interface segregation: separate trait refs ──────────────────

    #[test]
    fn producer_and_consumer_are_independent_traits() {
        let buf = MockRingBuffer::new(4);

        // A caller that only has a producer reference cannot pop.
        let producer: &dyn EventRingBufferProducer = &buf;
        producer.push(note_on(1)).unwrap();

        // A caller that only has a consumer reference cannot push.
        let consumer: &dyn EventRingBufferConsumer = &buf;
        let msg = consumer.pop().expect("should get the message");
        assert_eq!(msg.sequence_number(), 1);
    }
}
