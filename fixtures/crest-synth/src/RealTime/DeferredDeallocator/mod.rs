use std::any::Any;

pub trait DeferredDeallocator {
    fn retire(&self, item: Box<dyn Any + Send>);
    fn collect(&self);
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Arc, Mutex};

    struct MockDeallocator {
        retired_count: Arc<Mutex<usize>>,
        collected: Arc<Mutex<bool>>,
    }

    impl MockDeallocator {
        fn new() -> Self {
            Self {
                retired_count: Arc::new(Mutex::new(0)),
                collected: Arc::new(Mutex::new(false)),
            }
        }

        fn retired_count(&self) -> usize {
            *self.retired_count.lock().unwrap()
        }

        fn was_collected(&self) -> bool {
            *self.collected.lock().unwrap()
        }
    }

    impl DeferredDeallocator for MockDeallocator {
        fn retire(&self, _item: Box<dyn Any + Send>) {
            *self.retired_count.lock().unwrap() += 1;
        }

        fn collect(&self) {
            *self.collected.lock().unwrap() = true;
        }
    }

    #[test]
    fn retire_increments_count() {
        let dealloc = MockDeallocator::new();
        dealloc.retire(Box::new(42u32));
        dealloc.retire(Box::new(String::from("hello")));
        assert_eq!(dealloc.retired_count(), 2);
    }

    #[test]
    fn collect_marks_collected() {
        let dealloc = MockDeallocator::new();
        assert!(!dealloc.was_collected());
        dealloc.collect();
        assert!(dealloc.was_collected());
    }

    #[test]
    fn retire_accepts_different_types() {
        let dealloc = MockDeallocator::new();
        dealloc.retire(Box::new(vec![1, 2, 3]));
        dealloc.retire(Box::new((1.0f64, 2.0f64)));
        assert_eq!(dealloc.retired_count(), 2);
    }
}
