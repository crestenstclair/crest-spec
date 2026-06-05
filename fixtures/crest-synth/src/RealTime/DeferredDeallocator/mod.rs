/// A lock-free deferred deallocation port for real-time audio threads.
///
/// The audio thread calls [`DeferredDeallocator::retire`] to hand off owned
/// memory it no longer needs.  A background or UI thread periodically calls
/// [`DeferredDeallocator::collect`] to actually free the retired items.
///
/// # Real-time safety
///
/// Implementations MUST guarantee that `retire` is lock-free and performs no
/// heap deallocation.  All deallocation happens inside `collect`, which runs
/// on a non-RT thread.
pub trait DeferredDeallocator: Send + Sync {
    /// Hand off owned memory from the audio thread.
    ///
    /// This method MUST be lock-free and allocation-free so it is safe to
    /// call from a real-time audio callback.  The item will be held until a
    /// non-RT thread calls [`collect`](DeferredDeallocator::collect).
    fn retire(&self, item: Box<dyn std::any::Any + Send>);

    /// Drain and drop all previously retired items.
    ///
    /// Called periodically from a background or UI thread.  Each retired
    /// item is dropped (and therefore deallocated) during this call.
    fn collect(&self);
}

#[cfg(test)]
mod tests {
    use super::*;

    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::Arc;

    // ── Mock implementation ────────────────────────────────────────

    /// A simple mock that uses a `Mutex`-guarded `Vec` as its retire queue.
    ///
    /// This is NOT real-time safe — it exists only to verify the trait
    /// contract in unit tests.  A production implementation would use a
    /// lock-free SPSC or MPSC queue (e.g. `rtrb`, `basedrop`).
    struct MockDeallocator {
        queue: std::sync::Mutex<Vec<Box<dyn std::any::Any + Send>>>,
    }

    impl MockDeallocator {
        fn new() -> Self {
            Self {
                queue: std::sync::Mutex::new(Vec::new()),
            }
        }

        fn pending_count(&self) -> usize {
            self.queue.lock().unwrap().len()
        }
    }

    impl DeferredDeallocator for MockDeallocator {
        fn retire(&self, item: Box<dyn std::any::Any + Send>) {
            self.queue.lock().unwrap().push(item);
        }

        fn collect(&self) {
            let mut q = self.queue.lock().unwrap();
            q.clear();
        }
    }

    // ── A drop-tracking helper ─────────────────────────────────────

    /// Increments a shared counter when dropped, letting tests verify that
    /// `collect` actually frees memory.
    struct DropSentinel {
        counter: Arc<AtomicUsize>,
    }

    impl DropSentinel {
        fn new(counter: Arc<AtomicUsize>) -> Self {
            Self { counter }
        }
    }

    impl Drop for DropSentinel {
        fn drop(&mut self) {
            self.counter.fetch_add(1, Ordering::SeqCst);
        }
    }

    // ── Tests ──────────────────────────────────────────────────────

    #[test]
    fn retire_enqueues_item() {
        let d = MockDeallocator::new();
        assert_eq!(d.pending_count(), 0);

        d.retire(Box::new(42u32));

        assert_eq!(d.pending_count(), 1);
    }

    #[test]
    fn collect_drains_all_retired_items() {
        let d = MockDeallocator::new();

        d.retire(Box::new(1u64));
        d.retire(Box::new(2u64));
        d.retire(Box::new(3u64));
        assert_eq!(d.pending_count(), 3);

        d.collect();

        assert_eq!(d.pending_count(), 0);
    }

    #[test]
    fn collect_on_empty_queue_is_noop() {
        let d = MockDeallocator::new();
        d.collect(); // must not panic
        assert_eq!(d.pending_count(), 0);
    }

    #[test]
    fn collect_actually_drops_items() {
        let drop_count = Arc::new(AtomicUsize::new(0));
        let d = MockDeallocator::new();

        d.retire(Box::new(DropSentinel::new(Arc::clone(&drop_count))));
        d.retire(Box::new(DropSentinel::new(Arc::clone(&drop_count))));

        assert_eq!(drop_count.load(Ordering::SeqCst), 0);

        d.collect();

        assert_eq!(drop_count.load(Ordering::SeqCst), 2);
    }

    #[test]
    fn retire_accepts_heterogeneous_types() {
        let d = MockDeallocator::new();

        d.retire(Box::new(String::from("hello")));
        d.retire(Box::new(vec![1u8, 2, 3]));
        d.retire(Box::new(3.14f64));
        d.retire(Box::new(()));

        assert_eq!(d.pending_count(), 4);

        d.collect();

        assert_eq!(d.pending_count(), 0);
    }

    #[test]
    fn multiple_collect_cycles() {
        let drop_count = Arc::new(AtomicUsize::new(0));
        let d = MockDeallocator::new();

        // First cycle
        d.retire(Box::new(DropSentinel::new(Arc::clone(&drop_count))));
        d.collect();
        assert_eq!(drop_count.load(Ordering::SeqCst), 1);

        // Second cycle
        d.retire(Box::new(DropSentinel::new(Arc::clone(&drop_count))));
        d.retire(Box::new(DropSentinel::new(Arc::clone(&drop_count))));
        d.collect();
        assert_eq!(drop_count.load(Ordering::SeqCst), 3);
    }

    #[test]
    fn trait_is_object_safe() {
        // Verify DeferredDeallocator can be used as a trait object.
        let d: Box<dyn DeferredDeallocator> = Box::new(MockDeallocator::new());
        d.retire(Box::new(99i32));
        d.collect();
    }

    #[test]
    fn trait_object_behind_arc() {
        // Verify the trait works behind Arc (typical multi-thread sharing).
        let d: Arc<dyn DeferredDeallocator> = Arc::new(MockDeallocator::new());
        d.retire(Box::new("shared".to_string()));
        d.collect();
    }
}
