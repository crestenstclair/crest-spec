use crate::Modulation::LfoConfig::{LfoConfig, LfoWaveform};
use crate::Modulation::ModMatrix::ModMatrix;
use crate::Modulation::ModDestinationType::ModDestinationType;
use crate::Modulation::ModSourceType::ModSourceType;
use crate::Modulation::PerNoteExpression::PerNoteExpression;

const MAX_LFOS: usize = 8;
const MAX_MOD_ENVELOPES: usize = 4;

#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct ModulationOutput {
    pub pitch_mod: f64,
    pub filter_cutoff_mod: f64,
    pub filter_resonance_mod: f64,
    pub gain_mod: f64,
    pub pan_mod: f64,
}

#[derive(Debug, Clone, Copy, Default)]
pub struct EnvelopeState {
    pub stage: u8,
    pub level: f64,
}

pub struct ModulationProcessor {
    lfo_phases: [f64; MAX_LFOS],
    envelope_states: [EnvelopeState; MAX_MOD_ENVELOPES],
    pub sample_rate: f64,
}

impl ModulationProcessor {
    pub fn new(sample_rate: f64) -> Self {
        Self {
            lfo_phases: [0.0; MAX_LFOS],
            envelope_states: [EnvelopeState::default(); MAX_MOD_ENVELOPES],
            sample_rate,
        }
    }

    pub fn process(
        &mut self,
        matrix: &ModMatrix,
        expression: &PerNoteExpression,
        velocity: f64,
        note_number: f64,
    ) -> ModulationOutput {
        let mut output = ModulationOutput::default();

        let lfo_configs = matrix.lfo_configs();

        for routing in matrix.routings() {
            let source_value = match routing.source {
                ModSourceType::Lfo => {
                    if !lfo_configs.is_empty() {
                        compute_lfo_sample(&lfo_configs[0], self.lfo_phases[0])
                    } else {
                        0.0
                    }
                }
                ModSourceType::Envelope => {
                    self.envelope_states[0].level
                }
                ModSourceType::Velocity => velocity,
                ModSourceType::KeyTrack => (note_number - 60.0) / 60.0,
                ModSourceType::PerNoteBendX => expression.bend_x * 2.0 - 1.0,
                ModSourceType::PerNoteTimbreY => expression.timbre_y,
                ModSourceType::PerNotePressureZ => expression.pressure_z,
                ModSourceType::Random => 0.5,
                ModSourceType::Macro => 0.0,
            };

            let contribution = source_value * routing.depth;

            match routing.destination {
                ModDestinationType::Pitch => output.pitch_mod += contribution,
                ModDestinationType::FilterCutoff => output.filter_cutoff_mod += contribution,
                ModDestinationType::FilterResonance => output.filter_resonance_mod += contribution,
                ModDestinationType::Gain => output.gain_mod += contribution,
                ModDestinationType::Pan => output.pan_mod += contribution,
            }
        }

        output
    }

    pub fn advance_lfos(&mut self, matrix: &ModMatrix) {
        let lfo_configs = matrix.lfo_configs();
        let count = lfo_configs.len().min(MAX_LFOS);
        for i in 0..count {
            let rate = lfo_configs[i].rate;
            let increment = rate / self.sample_rate;
            self.lfo_phases[i] = (self.lfo_phases[i] + increment) % 1.0;
        }
    }

    pub fn note_on(&mut self) {
        for state in self.envelope_states.iter_mut() {
            state.stage = 1;
            state.level = 0.0;
        }
    }

    pub fn note_off(&mut self) {
        for state in self.envelope_states.iter_mut() {
            if state.stage != 0 {
                state.stage = 4;
            }
        }
    }

    pub fn reset(&mut self) {
        self.lfo_phases = [0.0; MAX_LFOS];
        self.envelope_states = [EnvelopeState::default(); MAX_MOD_ENVELOPES];
    }
}

fn compute_lfo_sample(config: &LfoConfig, phase: f64) -> f64 {
    use std::f64::consts::TAU;
    let phase_with_offset = (phase + config.phase) % 1.0;
    let raw = match config.waveform {
        LfoWaveform::Sine => (phase_with_offset * TAU).sin(),
        LfoWaveform::Triangle => {
            if phase_with_offset < 0.5 {
                phase_with_offset * 4.0 - 1.0
            } else {
                3.0 - phase_with_offset * 4.0
            }
        }
        LfoWaveform::Saw => phase_with_offset * 2.0 - 1.0,
        LfoWaveform::Square => {
            if phase_with_offset < 0.5 { 1.0 } else { -1.0 }
        }
        LfoWaveform::SampleAndHold => {
            if phase_with_offset < 0.5 { 1.0 } else { -1.0 }
        }
    };
    raw * config.depth
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Patch::PatchId::PatchId;

    fn default_expression() -> PerNoteExpression {
        PerNoteExpression::default()
    }

    fn empty_matrix() -> ModMatrix {
        ModMatrix::new(PatchId::new(0))
    }

    #[test]
    fn process_with_no_routings_returns_default_output() {
        let mut processor = ModulationProcessor::new(44100.0);
        let matrix = empty_matrix();
        let expression = default_expression();

        let output = processor.process(&matrix, &expression, 0.8, 60.0);
        assert_eq!(output, ModulationOutput::default());
    }

    #[test]
    fn velocity_to_gain_routing_produces_expected_gain_mod() {
        let mut processor = ModulationProcessor::new(44100.0);
        let mut matrix = ModMatrix::new(PatchId::new(0));
        matrix.add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 0.5).unwrap();
        let expression = default_expression();

        let output = processor.process(&matrix, &expression, 0.8, 60.0);
        let expected_gain_mod = 0.8 * 0.5;
        assert!((output.gain_mod - expected_gain_mod).abs() < 1e-10);
    }

    #[test]
    fn lfo_to_pitch_routing_at_zero_phase() {
        let mut processor = ModulationProcessor::new(44100.0);
        let mut matrix = ModMatrix::new(PatchId::new(0));
        matrix.add_routing(ModSourceType::Lfo, ModDestinationType::Pitch, 1.0).unwrap();
        matrix.configure_lfo(0, LfoConfig::default()).unwrap();
        let expression = default_expression();

        let output = processor.process(&matrix, &expression, 0.8, 60.0);
        assert!((output.pitch_mod).abs() < 1e-10);
    }

    #[test]
    fn lfo_to_pitch_routing_at_quarter_phase() {
        let mut processor = ModulationProcessor::new(44100.0);
        processor.lfo_phases[0] = 0.25;
        let mut matrix = ModMatrix::new(PatchId::new(0));
        matrix.add_routing(ModSourceType::Lfo, ModDestinationType::Pitch, 1.0).unwrap();
        let lfo = LfoConfig::new(LfoWaveform::Sine, 1.0, 1.0, false, 0.0).unwrap();
        matrix.configure_lfo(0, lfo).unwrap();
        let expression = default_expression();

        let output = processor.process(&matrix, &expression, 0.8, 60.0);
        assert!((output.pitch_mod - 1.0).abs() < 1e-10);
    }

    #[test]
    fn note_on_sets_envelopes_to_attack_stage() {
        let mut processor = ModulationProcessor::new(44100.0);
        processor.note_on();
        for state in processor.envelope_states.iter() {
            assert_eq!(state.stage, 1);
        }
    }

    #[test]
    fn note_off_sets_active_envelopes_to_release() {
        let mut processor = ModulationProcessor::new(44100.0);
        processor.note_on();
        processor.note_off();
        for state in processor.envelope_states.iter() {
            assert_eq!(state.stage, 4);
        }
    }

    #[test]
    fn note_off_does_not_change_idle_envelopes() {
        let mut processor = ModulationProcessor::new(44100.0);
        processor.note_off();
        for state in processor.envelope_states.iter() {
            assert_eq!(state.stage, 0);
        }
    }

    #[test]
    fn reset_clears_all_state() {
        let mut processor = ModulationProcessor::new(44100.0);
        processor.lfo_phases[0] = 0.75;
        processor.note_on();
        processor.reset();
        for phase in processor.lfo_phases.iter() {
            assert_eq!(*phase, 0.0);
        }
        for state in processor.envelope_states.iter() {
            assert_eq!(state.stage, 0);
        }
    }

    #[test]
    fn key_track_centered_at_middle_c() {
        let mut processor = ModulationProcessor::new(44100.0);
        let mut matrix = ModMatrix::new(PatchId::new(0));
        matrix.add_routing(ModSourceType::KeyTrack, ModDestinationType::FilterCutoff, 1.0).unwrap();
        let expression = default_expression();

        let output = processor.process(&matrix, &expression, 0.8, 60.0);
        assert!((output.filter_cutoff_mod).abs() < 1e-10);

        let output_high = processor.process(&matrix, &expression, 0.8, 72.0);
        assert!((output_high.filter_cutoff_mod - 0.2).abs() < 1e-10);
    }
}
