use crate::kernel::Amplitude::Amplitude;
use crate::Synth::OscillatorConfig::OscillatorConfig;
use crate::Synth::FilterConfig::FilterConfig;
use crate::Synth::AmpEnvelopeConfig::AmpEnvelopeConfig;
use crate::Patch::PatchId::PatchId;
use crate::Patch::ChannelSubscription::ChannelSubscription;
use crate::Patch::VoicePoolConfig::VoicePoolConfig;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EngineType {
    Subtractive,
    Fm,
    Wavetable,
}

impl Default for EngineType {
    fn default() -> Self {
        EngineType::Subtractive
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PatchCommand {
    CreatePatch,
    UpdateSubscription,
    UpdateOscillator,
    UpdateFilter,
    UpdateEnvelope,
    SetGain,
    SetPan,
    ActivatePatch,
    DeactivatePatch,
}

#[derive(Debug, Clone)]
pub enum PatchEvent {
    PatchCreated { id: PatchId, name: String, engine_type: EngineType },
    SubscriptionChanged { id: PatchId, subscription: ChannelSubscription },
    PatchParametersUpdated { id: PatchId },
    PatchActivated { id: PatchId },
    PatchDeactivated { id: PatchId },
}

#[derive(Debug)]
pub struct Patch {
    id: PatchId,
    name: String,
    engine_type: EngineType,
    oscillator: OscillatorConfig,
    filter: FilterConfig,
    amp_envelope: AmpEnvelopeConfig,
    subscription: ChannelSubscription,
    voice_pool_config: VoicePoolConfig,
    gain: Amplitude,
    pan: f64,
    active: bool,
}

impl Patch {
    pub fn new(
        id: PatchId,
        name: String,
        engine_type: EngineType,
        subscription: ChannelSubscription,
    ) -> Self {
        Self {
            id,
            name,
            engine_type,
            oscillator: OscillatorConfig::default(),
            filter: FilterConfig::default(),
            amp_envelope: AmpEnvelopeConfig::default(),
            subscription,
            voice_pool_config: VoicePoolConfig::default(),
            gain: Amplitude::unity(),
            pan: 0.0,
            active: true,
        }
    }

    pub fn id(&self) -> PatchId {
        self.id
    }

    pub fn name(&self) -> &str {
        &self.name
    }

    pub fn engine_type(&self) -> EngineType {
        self.engine_type
    }

    pub fn oscillator(&self) -> &OscillatorConfig {
        &self.oscillator
    }

    pub fn filter(&self) -> &FilterConfig {
        &self.filter
    }

    pub fn amp_envelope(&self) -> &AmpEnvelopeConfig {
        &self.amp_envelope
    }

    pub fn subscription(&self) -> &ChannelSubscription {
        &self.subscription
    }

    pub fn voice_pool_config(&self) -> VoicePoolConfig {
        self.voice_pool_config
    }

    pub fn gain(&self) -> Amplitude {
        self.gain
    }

    pub fn pan(&self) -> f64 {
        self.pan
    }

    pub fn is_active(&self) -> bool {
        self.active
    }

    pub fn update_subscription(&mut self, subscription: ChannelSubscription) -> PatchEvent {
        self.subscription = subscription;
        PatchEvent::SubscriptionChanged {
            id: self.id,
            subscription,
        }
    }

    pub fn update_oscillator(&mut self, config: OscillatorConfig) -> PatchEvent {
        self.oscillator = config;
        PatchEvent::PatchParametersUpdated { id: self.id }
    }

    pub fn update_filter(&mut self, config: FilterConfig) -> PatchEvent {
        self.filter = config;
        PatchEvent::PatchParametersUpdated { id: self.id }
    }

    pub fn update_envelope(&mut self, config: AmpEnvelopeConfig) -> PatchEvent {
        self.amp_envelope = config;
        PatchEvent::PatchParametersUpdated { id: self.id }
    }

    pub fn set_gain(&mut self, gain: Amplitude) -> PatchEvent {
        self.gain = gain;
        PatchEvent::PatchParametersUpdated { id: self.id }
    }

    pub fn set_pan(&mut self, pan: f64) -> Result<PatchEvent, &'static str> {
        if !(-1.0..=1.0).contains(&pan) {
            return Err("pan must be -1.0 (left) to 1.0 (right)");
        }
        self.pan = pan;
        Ok(PatchEvent::PatchParametersUpdated { id: self.id })
    }

    pub fn activate(&mut self) -> PatchEvent {
        self.active = true;
        PatchEvent::PatchActivated { id: self.id }
    }

    pub fn deactivate(&mut self) -> PatchEvent {
        self.active = false;
        PatchEvent::PatchDeactivated { id: self.id }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::kernel::MidiGroup::MidiGroup;
    use crate::kernel::MidiChannel::MidiChannel;
    use crate::kernel::ChannelAddress::ChannelAddress;

    fn test_subscription() -> ChannelSubscription {
        let addr = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(0).unwrap(),
        );
        ChannelSubscription::single(addr)
    }

    fn test_patch() -> Patch {
        Patch::new(
            PatchId::new(1),
            "Test Patch".to_string(),
            EngineType::Subtractive,
            test_subscription(),
        )
    }

    #[test]
    fn create_patch() {
        let patch = test_patch();
        assert_eq!(patch.id(), PatchId::new(1));
        assert_eq!(patch.name(), "Test Patch");
        assert_eq!(patch.engine_type(), EngineType::Subtractive);
        assert!(patch.is_active());
        assert!((patch.pan()).abs() < f64::EPSILON);
    }

    #[test]
    fn default_engine_type() {
        assert_eq!(EngineType::default(), EngineType::Subtractive);
    }

    #[test]
    fn update_subscription() {
        let mut patch = test_patch();
        let new_addr = ChannelAddress::new(
            MidiGroup::new(0).unwrap(),
            MidiChannel::new(5).unwrap(),
        );
        let new_sub = ChannelSubscription::single(new_addr);
        let event = patch.update_subscription(new_sub);
        assert_eq!(patch.subscription().address().channel().value(), 5);
        assert!(matches!(event, PatchEvent::SubscriptionChanged { .. }));
    }

    #[test]
    fn set_pan_valid() {
        let mut patch = test_patch();
        patch.set_pan(-1.0).unwrap();
        assert!((patch.pan() - (-1.0)).abs() < f64::EPSILON);

        patch.set_pan(1.0).unwrap();
        assert!((patch.pan() - 1.0).abs() < f64::EPSILON);

        patch.set_pan(0.0).unwrap();
        assert!((patch.pan()).abs() < f64::EPSILON);
    }

    #[test]
    fn set_pan_invalid() {
        let mut patch = test_patch();
        assert!(patch.set_pan(-1.1).is_err());
        assert!(patch.set_pan(1.1).is_err());
    }

    #[test]
    fn set_gain() {
        let mut patch = test_patch();
        let gain = Amplitude::new(0.5).unwrap();
        let event = patch.set_gain(gain);
        assert!((patch.gain().value() - 0.5).abs() < f64::EPSILON);
        assert!(matches!(event, PatchEvent::PatchParametersUpdated { .. }));
    }

    #[test]
    fn activate_deactivate() {
        let mut patch = test_patch();
        assert!(patch.is_active());

        let event = patch.deactivate();
        assert!(!patch.is_active());
        assert!(matches!(event, PatchEvent::PatchDeactivated { .. }));

        let event = patch.activate();
        assert!(patch.is_active());
        assert!(matches!(event, PatchEvent::PatchActivated { .. }));
    }

    #[test]
    fn update_oscillator() {
        let mut patch = test_patch();
        use crate::Synth::OscillatorConfig::Waveform;
        let config = OscillatorConfig::new(Waveform::Square, 5.0, 0.3);
        let event = patch.update_oscillator(config);
        assert_eq!(patch.oscillator().waveform, Waveform::Square);
        assert!(matches!(event, PatchEvent::PatchParametersUpdated { .. }));
    }

    #[test]
    fn update_filter() {
        let mut patch = test_patch();
        use crate::kernel::Frequency::Frequency;
        use crate::Synth::FilterConfig::FilterType;
        let config = FilterConfig::new(
            Frequency::new(2000.0).unwrap(),
            0.5,
            FilterType::HighPass,
        ).unwrap();
        let event = patch.update_filter(config);
        assert_eq!(patch.filter().filter_type, FilterType::HighPass);
        assert!(matches!(event, PatchEvent::PatchParametersUpdated { .. }));
    }

    #[test]
    fn update_envelope() {
        let mut patch = test_patch();
        let config = AmpEnvelopeConfig::new(0.05, 0.2, 0.5, 0.5).unwrap();
        let event = patch.update_envelope(config);
        assert!((patch.amp_envelope().attack - 0.05).abs() < f64::EPSILON);
        assert!(matches!(event, PatchEvent::PatchParametersUpdated { .. }));
    }
}
