use crate::RealTime::ParameterSnapshot::ParameterSnapshot;

pub trait ParameterBridgeWriter: Send + Sync {
    fn write(&self, snapshot: ParameterSnapshot);
}

pub trait ParameterBridgeReader: Send + Sync {
    fn read(&self) -> ParameterSnapshot;
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};

    struct MockParameterBridgeWriter {
        last_written: std::sync::Mutex<Option<ParameterSnapshot>>,
        write_count: AtomicU64,
    }

    impl MockParameterBridgeWriter {
        fn new() -> Self {
            Self {
                last_written: std::sync::Mutex::new(None),
                write_count: AtomicU64::new(0),
            }
        }

        fn last_written(&self) -> Option<ParameterSnapshot> {
            *self.last_written.lock().unwrap()
        }

        fn write_count(&self) -> u64 {
            self.write_count.load(Ordering::Relaxed)
        }
    }

    impl ParameterBridgeWriter for MockParameterBridgeWriter {
        fn write(&self, snapshot: ParameterSnapshot) {
            *self.last_written.lock().unwrap() = Some(snapshot);
            self.write_count.fetch_add(1, Ordering::Relaxed);
        }
    }

    struct MockParameterBridgeReader {
        snapshot: ParameterSnapshot,
    }

    impl MockParameterBridgeReader {
        fn new(snapshot: ParameterSnapshot) -> Self {
            Self { snapshot }
        }
    }

    impl ParameterBridgeReader for MockParameterBridgeReader {
        fn read(&self) -> ParameterSnapshot {
            self.snapshot
        }
    }

    #[test]
    fn writer_stores_snapshot() {
        let writer = MockParameterBridgeWriter::new();
        let snapshot = ParameterSnapshot::default();

        writer.write(snapshot);

        assert_eq!(writer.write_count(), 1);
        assert!(writer.last_written().is_some());
    }

    #[test]
    fn writer_tracks_multiple_writes() {
        let writer = MockParameterBridgeWriter::new();
        let snapshot = ParameterSnapshot::default();

        writer.write(snapshot);
        writer.write(snapshot);
        writer.write(snapshot);

        assert_eq!(writer.write_count(), 3);
    }

    #[test]
    fn reader_returns_snapshot() {
        let snapshot = ParameterSnapshot::default();
        let reader = MockParameterBridgeReader::new(snapshot);

        let result = reader.read();

        assert_eq!(result, snapshot);
    }

    #[test]
    fn reader_returns_same_snapshot_on_repeated_reads() {
        let snapshot = ParameterSnapshot::default();
        let reader = MockParameterBridgeReader::new(snapshot);

        let first = reader.read();
        let second = reader.read();

        assert_eq!(first, second);
    }

    #[test]
    fn writer_is_send_and_sync() {
        fn assert_send_sync<T: Send + Sync>() {}
        assert_send_sync::<MockParameterBridgeWriter>();
    }

    #[test]
    fn reader_is_send_and_sync() {
        fn assert_send_sync<T: Send + Sync>() {}
        assert_send_sync::<MockParameterBridgeReader>();
    }

    #[test]
    fn traits_are_object_safe() {
        fn accept_writer(_w: &dyn ParameterBridgeWriter) {}
        fn accept_reader(_r: &dyn ParameterBridgeReader) {}

        let writer = MockParameterBridgeWriter::new();
        let snapshot = ParameterSnapshot::default();
        let reader = MockParameterBridgeReader::new(snapshot);

        accept_writer(&writer);
        accept_reader(&reader);
    }
}
