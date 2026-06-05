// path: tests/Synth/Voice/mod.rs

#[cfg(test)]
mod tests {
    use crest_synth::kernel::NoteId::NoteId;
    use crest_synth::kernel::NoteNumber::NoteNumber;
    use crest_synth::kernel::Velocity::Velocity;
    use crest_synth::Synth::EnvelopeStage::EnvelopeStage;
    use crest_synth::Synth::Voice::{
        FilterState, NoteOff, NoteOn, Voice, VoiceActivated, VoiceReleased, VoiceStolen,
    };

    // -- helpers --------------------------------------------------------------

    fn middle_c_on(note_id: u32) -> NoteOn {
        NoteOn {
            note_id: NoteId::new(note_id),
            note_number: NoteNumber::new(60).unwrap(),
            velocity: Velocity::new(0.8).unwrap(),
        }
    }

    fn a4_on(note_id: u32) -> NoteOn {
        NoteOn {
            note_id: NoteId::new(note_id),
            note_number: NoteNumber::new(69).unwrap(),
            velocity: Velocity::new(1.0).unwrap(),
        }
    }

    fn note_off(note_id: u32) -> NoteOff {
        NoteOff {
            note_id: NoteId::new(note_id),
        }
    }

    // -- new() ----------------------------------------------------------------

    #[test]
    fn new_voice_is_idle() {
        let voice = Voice::new();
        assert!(voice.is_idle());
    }

    #[test]
    fn new_voice_is_not_active() {
        let voice = Voice::new();
        assert!(!voice.is_active());
    }

    #[test]
    fn new_voice_envelope_stage_is_idle() {
        let voice = Voice::new();
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Idle);
    }

    #[test]
    fn new_voice_filter_state_is_default() {
        let voice = Voice::new();
        assert_eq!(voice.filter_state(), FilterState::default());
    }

    #[test]
    fn new_voice_oscillator_phase_is_zero() {
        let voice = Voice::new();
        assert_eq!(voice.oscillator_phase(), 0.0);
    }

    // -- note_on() ------------------------------------------------------------

    #[test]
    fn note_on_activates_voice() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        assert!(voice.is_active());
    }

    #[test]
    fn note_on_leaves_idle_state() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        assert!(!voice.is_idle());
    }

    #[test]
    fn note_on_sets_attack_stage() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Attack);
    }

    #[test]
    fn note_on_stores_note_id() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(42));
        assert_eq!(voice.note_id(), NoteId::new(42));
    }

    #[test]
    fn note_on_stores_note_number() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        assert_eq!(voice.note_number(), NoteNumber::new(60).unwrap());
    }

    #[test]
    fn note_on_stores_velocity() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        assert_eq!(voice.velocity(), Velocity::new(0.8).unwrap());
    }

    #[test]
    fn note_on_resets_oscillator_phase() {
        let mut voice = Voice::new();
        // Activate once, then activate again; phase must reset.
        voice.note_on(middle_c_on(1));
        voice.note_on(middle_c_on(2));
        assert_eq!(voice.oscillator_phase(), 0.0);
    }

    #[test]
    fn note_on_resets_filter_state() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        assert_eq!(voice.filter_state(), FilterState::default());
    }

    #[test]
    fn note_on_returns_activated_event() {
        let mut voice = Voice::new();
        let event = voice.note_on(middle_c_on(7));
        assert_eq!(event.note_id, NoteId::new(7));
        assert_eq!(event.note_number, NoteNumber::new(60).unwrap());
    }

    // -- frequency derivation -------------------------------------------------

    #[test]
    fn a4_produces_440_hz() {
        let mut voice = Voice::new();
        let event = voice.note_on(a4_on(1));
        let hz = event.frequency.value();
        assert!(
            (hz - 440.0).abs() < 1e-10,
            "Expected 440.0 Hz for A4, got {hz}"
        );
    }

    #[test]
    fn middle_c_frequency_is_approximately_261_63() {
        let mut voice = Voice::new();
        let event = voice.note_on(middle_c_on(1));
        let hz = event.frequency.value();
        assert!(
            (hz - 261.625_565_300_6).abs() < 0.01,
            "Expected ~261.63 Hz for middle C, got {hz}"
        );
    }

    #[test]
    fn octave_doubles_frequency() {
        let mut voice = Voice::new();
        let low = voice.note_on(NoteOn {
            note_id: NoteId::new(1),
            note_number: NoteNumber::new(60).unwrap(),
            velocity: Velocity::new(0.5).unwrap(),
        });
        let high = voice.note_on(NoteOn {
            note_id: NoteId::new(2),
            note_number: NoteNumber::new(72).unwrap(),
            velocity: Velocity::new(0.5).unwrap(),
        });
        let ratio = high.frequency.value() / low.frequency.value();
        assert!(
            (ratio - 2.0).abs() < 1e-10,
            "Octave should double frequency, ratio was {ratio}"
        );
    }

    // -- note_off() -----------------------------------------------------------

    #[test]
    fn note_off_matching_id_returns_released() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(5));
        let released = voice.note_off(note_off(5));
        assert!(released.is_some());
        assert_eq!(released.unwrap().note_id, NoteId::new(5));
    }

    #[test]
    fn note_off_matching_id_sets_release_stage() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(5));
        voice.note_off(note_off(5));
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Release);
    }

    #[test]
    fn note_off_mismatched_id_returns_none() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(5));
        let released = voice.note_off(note_off(999));
        assert!(released.is_none());
    }

    #[test]
    fn note_off_mismatched_id_does_not_change_stage() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(5));
        voice.note_off(note_off(999));
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Attack);
    }

    // -- steal() --------------------------------------------------------------

    #[test]
    fn steal_returns_stolen_event_with_correct_ids() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(10));
        let (stolen, _activated) = voice.steal(a4_on(20));
        assert_eq!(stolen.old_note_id, NoteId::new(10));
        assert_eq!(stolen.new_note_id, NoteId::new(20));
    }

    #[test]
    fn steal_returns_activated_event_for_new_note() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(10));
        let (_stolen, activated) = voice.steal(a4_on(20));
        assert_eq!(activated.note_id, NoteId::new(20));
        assert_eq!(activated.note_number, NoteNumber::new(69).unwrap());
    }

    #[test]
    fn steal_activates_voice_with_new_note() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(10));
        voice.steal(a4_on(20));
        assert_eq!(voice.note_id(), NoteId::new(20));
        assert!(voice.is_active());
        assert_eq!(voice.envelope_stage(), EnvelopeStage::Attack);
    }

    #[test]
    fn steal_resets_phase_and_filter() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(10));
        voice.steal(a4_on(20));
        assert_eq!(voice.oscillator_phase(), 0.0);
        assert_eq!(voice.filter_state(), FilterState::default());
    }

    // -- is_idle / is_active edge cases ---------------------------------------

    #[test]
    fn voice_in_release_is_not_idle() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        voice.note_off(note_off(1));
        assert!(!voice.is_idle());
    }

    #[test]
    fn voice_in_release_is_still_active() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        voice.note_off(note_off(1));
        assert!(voice.is_active());
    }

    // -- derive traits --------------------------------------------------------

    #[test]
    fn voice_is_clone() {
        let mut voice = Voice::new();
        voice.note_on(middle_c_on(1));
        let cloned = voice.clone();
        assert_eq!(voice, cloned);
    }

    #[test]
    fn voice_debug_output_is_nonempty() {
        let voice = Voice::new();
        let debug = format!("{:?}", voice);
        assert!(!debug.is_empty());
    }

    // -- FilterState ----------------------------------------------------------

    #[test]
    fn filter_state_default_is_zeroed() {
        let fs = FilterState::default();
        assert_eq!(fs.x1, 0.0);
        assert_eq!(fs.x2, 0.0);
        assert_eq!(fs.y1, 0.0);
        assert_eq!(fs.y2, 0.0);
    }

    #[test]
    fn filter_state_is_copy() {
        let a = FilterState {
            x1: 1.0,
            x2: 2.0,
            y1: 3.0,
            y2: 4.0,
        };
        let b = a;
        assert_eq!(a, b);
    }

    // -- command/event structs ------------------------------------------------

    #[test]
    fn note_on_is_debug_and_clone() {
        let cmd = middle_c_on(1);
        let cloned = cmd.clone();
        assert_eq!(cmd, cloned);
        let debug = format!("{:?}", cmd);
        assert!(!debug.is_empty());
    }

    #[test]
    fn note_off_is_debug_and_clone() {
        let cmd = note_off(1);
        let cloned = cmd.clone();
        assert_eq!(cmd, cloned);
        let debug = format!("{:?}", cmd);
        assert!(!debug.is_empty());
    }

    #[test]
    fn voice_activated_is_copy() {
        let mut voice = Voice::new();
        let event = voice.note_on(middle_c_on(1));
        let copy = event;
        assert_eq!(event, copy);
    }

    #[test]
    fn voice_released_is_copy() {
        let event = VoiceReleased {
            note_id: NoteId::new(1),
        };
        let copy = event;
        assert_eq!(event, copy);
    }

    #[test]
    fn voice_stolen_is_copy() {
        let event = VoiceStolen {
            old_note_id: NoteId::new(1),
            new_note_id: NoteId::new(2),
        };
        let copy = event;
        assert_eq!(event, copy);
    }
}
