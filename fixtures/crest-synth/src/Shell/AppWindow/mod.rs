pub struct WindowConfig {
    pub title: String,
    pub width: u32,
    pub height: u32,
}

impl WindowConfig {
    pub fn new(title: impl Into<String>, width: u32, height: u32) -> Self {
        Self {
            title: title.into(),
            width,
            height,
        }
    }
}

impl Default for WindowConfig {
    fn default() -> Self {
        Self {
            title: "crest-synth".to_string(),
            width: 800,
            height: 600,
        }
    }
}

pub trait FrameCallback: FnMut() {}
impl<F: FnMut()> FrameCallback for F {}

pub trait AppWindow {
    type Window;
    type Error: std::fmt::Debug;

    fn create(&mut self, config: WindowConfig) -> Result<Self::Window, Self::Error>;
    fn run_loop(&mut self, callback: Box<dyn FnMut()>) -> Result<(), Self::Error>;
}

#[cfg(test)]
mod tests {
    use super::*;

    struct MockWindow;

    struct MockAppWindow {
        created: bool,
        loop_ran: bool,
    }

    impl MockAppWindow {
        fn new() -> Self {
            Self {
                created: false,
                loop_ran: false,
            }
        }
    }

    impl AppWindow for MockAppWindow {
        type Window = MockWindow;
        type Error = &'static str;

        fn create(&mut self, _config: WindowConfig) -> Result<Self::Window, Self::Error> {
            self.created = true;
            Ok(MockWindow)
        }

        fn run_loop(&mut self, mut callback: Box<dyn FnMut()>) -> Result<(), Self::Error> {
            callback();
            self.loop_ran = true;
            Ok(())
        }
    }

    #[test]
    fn create_window() {
        let mut app = MockAppWindow::new();
        let config = WindowConfig::new("test", 640, 480);
        app.create(config).unwrap();
        assert!(app.created);
    }

    #[test]
    fn run_loop() {
        let mut app = MockAppWindow::new();
        app.run_loop(Box::new(|| {})).unwrap();
        assert!(app.loop_ran);
    }

    #[test]
    fn default_config() {
        let config = WindowConfig::default();
        assert_eq!(config.title, "crest-synth");
        assert_eq!(config.width, 800);
        assert_eq!(config.height, 600);
    }
}
