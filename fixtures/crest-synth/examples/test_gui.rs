use crest_synth::Modulation::LfoConfig::{LfoConfig, LfoWaveform};
use crest_synth::Modulation::ModDestinationType::ModDestinationType;
use crest_synth::Modulation::ModEnvelopeConfig::ModEnvelopeConfig;
use crest_synth::Modulation::ModMatrix::ModMatrix;
use crest_synth::Modulation::ModSourceType::ModSourceType;
use crest_synth::Modulation::PerNoteExpression::PerNoteExpression;
use crest_synth::Patch::PatchId::PatchId;
use eframe::egui;

fn main() -> eframe::Result<()> {
    let options = eframe::NativeOptions {
        viewport: egui::ViewportBuilder::default()
            .with_inner_size([700.0, 600.0]),
        ..Default::default()
    };

    eframe::run_simple_native(
        "crest-synth phase 5 test",
        options,
        move |ctx, _frame| {
            let patch_id = PatchId::new(0);
            let mut mod_matrix = ModMatrix::new(patch_id);

            mod_matrix
                .add_routing(ModSourceType::Lfo, ModDestinationType::Pitch, 0.5)
                .expect("add LFO->Pitch routing");
            mod_matrix
                .add_routing(ModSourceType::Envelope, ModDestinationType::FilterCutoff, -0.8)
                .expect("add Envelope->FilterCutoff routing");
            mod_matrix
                .add_routing(ModSourceType::Velocity, ModDestinationType::Gain, 1.0)
                .expect("add Velocity->Gain routing");

            mod_matrix
                .configure_lfo(
                    0,
                    LfoConfig::new(LfoWaveform::Triangle, 3.0, 0.7, false, 0.0)
                        .expect("create LfoConfig"),
                )
                .expect("configure LFO at index 0");

            mod_matrix
                .configure_mod_envelope(
                    0,
                    ModEnvelopeConfig::new(0.02, 0.2, 0.6, 0.4)
                        .expect("create ModEnvelopeConfig"),
                )
                .expect("configure mod envelope at index 0");

            let per_note_expression = PerNoteExpression::default();

            egui::CentralPanel::default().show(ctx, |ui| {
                ui.heading("Phase 5: Modulation — Routing Matrix");
                ui.add_space(8.0);

                ui.label("ModMatrix routings:");
                ui.monospace(format!("{:#?}", mod_matrix.routings()));
                ui.add_space(8.0);

                ui.label("LFO configs:");
                ui.monospace(format!("{:#?}", mod_matrix.lfo_configs()));
                ui.add_space(8.0);

                ui.label("Mod envelopes:");
                ui.monospace(format!("{:#?}", mod_matrix.mod_envelopes()));
                ui.add_space(8.0);

                ui.label("PerNoteExpression:");
                ui.monospace(format!("{:#?}", per_note_expression));
            });

            ctx.send_viewport_cmd(egui::ViewportCommand::Close);
        },
    )
}
