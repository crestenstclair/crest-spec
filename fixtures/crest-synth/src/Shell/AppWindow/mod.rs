pub struct WindowConfig {
    pub title: String,
    pub width: u32,
    pub height: u32,
}

pub struct Window {
    pub id: u64,
}

pub type FrameCallback = Box<dyn FnMut() + Send>;

pub trait AppWindow {
    fn create(&self, config: WindowConfig) -> Window;
    fn run_loop(&self, callback: FrameCallback);
}
