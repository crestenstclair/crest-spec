#!/usr/bin/env python3
"""Assemble fixtures/crest-synth/phases/phase-1..10 from the whole-picture spec.

Each phase directory is a COMPLETE, standalone, copy-paste-able CUE spec — the
crest-synth design as of that development phase (see DESIGN.md "Development
Phases"). Point CREST_SPEC_SPEC_DIR at any phase to build that stage; move it
to the next phase and the planner regenerates exactly the evolution.

Phase 10 must equal spec/ byte-for-byte (fixture_test.go enforces it), so the
snapshots can never drift from the authoritative whole-picture design. Re-run
this script after editing spec/.
"""

import os
import shutil
import sys

ROOT = os.path.join(os.path.dirname(__file__), "..", "fixtures", "crest-synth")
SPEC = os.path.join(ROOT, "spec")
PHASES = os.path.join(ROOT, "phases")


def read(name):
    with open(os.path.join(SPEC, name)) as f:
        return f.read()


def cut(content, block, label):
    assert block in content, f"{label}: block not found:\n{block[:120]}"
    return content.replace(block, "")


def swap(content, old, new, label):
    assert old in content, f"{label}: text not found: {old[:120]}"
    return content.replace(old, new)


# ── Variant: shell without GUI/gamepad (phases 1-9) ─────────────────────────

def shell_early():
    s = read("shell.cue")
    s = swap(
        s,
        "// Shell — infrastructure adapters for audio, MIDI, window/GUI, and gamepad.\n"
        "// The engine library stays host-agnostic; the shell is the only place that\n"
        "// touches devices and windows.",
        "// Shell — infrastructure adapters for audio and MIDI. The engine library\n"
        "// stays host-agnostic; the shell is the only place that touches devices.\n"
        "// Window/GUI/gamepad adapters arrive in phase 10.",
        "shell header",
    )
    s = swap(
        s,
        'project: contexts: Shell: purpose: "ports and adapters for the outside world: audio output, MIDI input, windowing/GUI, and gamepad navigation (every UI action reachable via gamepad)"',
        'project: contexts: Shell: purpose: "ports and adapters for the outside world: audio output and MIDI input (window/GUI/gamepad arrive in phase 10)"',
        "shell purpose",
    )
    s = cut(s, '\tGamepadAction: {description: "a mapped gamepad input: navigate (d-pad), fine-adjust (left stick), scroll (right stick), select (A), back (B), switch view (triggers), switch patch (bumpers), save session (start), open browser (select)"}\n', "GamepadAction")
    s = cut(s, """\tAppWindow: {
\t\tcontract: {
\t\t\trun: "(app: App) -> result<(), WindowError>"
\t\t}
\t}
\tGuiRenderer: {
\t\tcontract: {
\t\t\trender: "(view: ViewState) -> ()"
\t\t}
\t\tmeta: notes: "renders the patch editor, mixer view, preset browser, and mod matrix editor"
\t}
\tGamepadInput: {
\t\tcontract: {
\t\t\tpoll: "() -> list<GamepadAction>"
\t\t}
\t}
""", "GUI ports")
    s = cut(s, """
project: adapters: EframeAppWindow: {
\timplements: "port.Shell.AppWindow"
\tlayer:      "infrastructure"
\tmeta: framework: "eframe"
}

project: adapters: EguiRenderer: {
\timplements: "port.Shell.GuiRenderer"
\tlayer:      "infrastructure"
\tmeta: framework: "egui"
}

project: adapters: GilrsGamepadInput: {
\timplements: "port.Shell.GamepadInput"
\tlayer:      "infrastructure"
\tmeta: framework: "gilrs"
}
""", "GUI adapters")
    return s


# ── Variant: sample without SF2 (phase 6) ────────────────────────────────────

def sample_phase6():
    s = read("sample.cue")
    s = swap(s, "// Sample — sample playback and soundfonts with key/velocity zone mapping.",
             "// Sample — sample playback with key/velocity zone mapping. Soundfont (SF2)\n// support arrives in phase 7.", "sample header")
    s = swap(s,
             'project: contexts: Sample: purpose: "sample-based sound sources: WAV and SF2 loading, zone mapping by key and velocity range, pitched playback with interpolation and looping"',
             'project: contexts: Sample: purpose: "sample-based sound sources: WAV loading, zone mapping by key and velocity range, pitched playback with interpolation and looping"',
             "sample purpose")
    s = cut(s, '\t\t\tloadSf2: "(path: Path) -> result<list<SampleSet>, LoadError>"\n', "loadSf2")
    return s


# ── Variant: preset without Session (phase 9) ────────────────────────────────

def preset_phase9():
    s = read("preset.cue")
    s = swap(s,
             "// Preset — persistence. Presets snapshot one patch; banks collect presets;\n// sessions snapshot everything.",
             "// Preset — persistence. Presets snapshot one patch; banks collect presets.\n// Full-session snapshots arrive in phase 10.", "preset header")
    s = swap(s,
             'project: contexts: Preset: purpose: "save and restore: presets (one patch\'s complete config), banks (ordered preset collections), and sessions (a snapshot of all state)"',
             'project: contexts: Preset: purpose: "save and restore: presets (one patch\'s complete config) and banks (ordered preset collections); full sessions arrive in phase 10"',
             "preset purpose")
    session_block = s[s.index("project: contexts: Preset: aggregates: Session: {"):]
    session_block = session_block[:session_block.index("\n}\n") + 3]
    s = cut(s, "\n" + session_block, "Session aggregate")
    s = cut(s, """\tSessionManager: {
\t\tpurpose: "save, load, list, and delete full sessions"
\t\tuses: ["aggregate.Preset.Session", "port.Preset.PresetStorage"]
\t}
""", "SessionManager")
    return s


# ── Variant: project.cue per phase ────────────────────────────────────────────

CONTEXTS_BY_PHASE = {
    1: ["Kernel", "Shell"],
    2: ["Kernel", "Shell", "Engine"],
    3: ["Kernel", "Shell", "Engine", "RealTime"],
    4: ["Kernel", "Shell", "Engine", "RealTime", "Effects"],
    5: ["Kernel", "Shell", "Engine", "RealTime", "Effects", "Mixer"],
    6: ["Kernel", "Shell", "Engine", "RealTime", "Effects", "Mixer", "Sample"],
    7: ["Kernel", "Shell", "Engine", "RealTime", "Effects", "Mixer", "Sample"],
    8: ["Kernel", "Shell", "Engine", "RealTime", "Effects", "Mixer", "Sample", "Modulation", "Patch"],
    9: ["Kernel", "Shell", "Engine", "RealTime", "Effects", "Mixer", "Sample", "Modulation", "Patch", "Preset"],
    10: ["Kernel", "Shell", "Engine", "RealTime", "Effects", "Mixer", "Sample", "Modulation", "Patch", "Preset"],
}

# invariant text prefix → phase it first applies
INVARIANT_PHASE = {
    "the audio thread never allocates": 1,
    "the audio thread never acquires": 1,
    "the audio thread never performs blocking": 1,
    "all parameter changes cross the thread boundary": 3,
    "memory retired by the audio thread": 3,
    "signal flows engine output": 5,
    "insert chains process slots strictly": 4,
    "send taps are post-fader": 5,
    "a MidiEvent is dispatched": 8,
    "MPE zones never overlap": 8,
    "presets serialize with an explicit version": 9,
    "restoring a session replaces all state": 10,
}


def project_for_phase(n):
    s = read("project.cue")
    ctxs = set(CONTEXTS_BY_PHASE[n])

    # Filter architectural invariants to those applicable at this phase.
    out = []
    for line in s.split("\n"):
        stripped = line.strip()
        if stripped.startswith('{text: "'):
            text = stripped.split('{text: "', 1)[1]
            phase = next((p for prefix, p in INVARIANT_PHASE.items() if text.startswith(prefix)), 1)
            if phase > n:
                continue
        if stripped.startswith('{from: "') and ", to: " in stripped:
            frm = stripped.split('{from: "', 1)[1].split('"', 1)[0]
            to = stripped.split(', to: "', 1)[1].split('"', 1)[0]
            if frm not in ctxs or to not in ctxs:
                continue
        out.append(line)
    s = "\n".join(out)

    s = swap(s, "// crest-synth — reference spec for crest-spec.",
             f"// crest-synth — phase {n} of the evolving design (see ../../DESIGN.md).", f"project header p{n}")
    return s


# ── Variant: manifest per phase ───────────────────────────────────────────────

DEPS_BY_PHASE = {
    1: "cpal (audio output), midir (MIDI input)",
    3: "cpal (audio output), midir (MIDI input), rtrb (lock-free SPSC ring buffer), triple_buffer (lock-free latest-wins parameter sharing), basedrop (deferred deallocation for real-time)",
    6: "cpal (audio output), midir (MIDI input), rtrb (lock-free SPSC ring buffer), triple_buffer (lock-free latest-wins parameter sharing), basedrop (deferred deallocation for real-time), symphonia (audio file decoding)",
    9: "cpal (audio output), midir (MIDI input), rtrb (lock-free SPSC ring buffer), triple_buffer (lock-free latest-wins parameter sharing), basedrop (deferred deallocation for real-time), symphonia (audio file decoding), serde + serde_json (preset/session serialization)",
    10: "cpal (audio output), midir (MIDI input), eframe + egui (GUI), gilrs (gamepad input), rtrb (lock-free SPSC ring buffer), triple_buffer (lock-free latest-wins parameter sharing), basedrop (deferred deallocation for real-time), serde + serde_json (preset/session serialization), symphonia (audio file decoding)",
}

TONE_TEST_P1_USES = 'uses: ["valueObject.Kernel.Frequency", "valueObject.Kernel.AudioFrame"]'
TONE_TEST_P1_PROMPT = '"Generate a single A440 note with a throwaway inline sine oscillator (the real engine arrives in phase 2), render one second of audio into a buffer, and MEASURE the peak absolute sample value of the rendered buffer.",'


def manifest_for_phase(n):
    s = read("manifest.cue")
    tier = max(p for p in DEPS_BY_PHASE if p <= n)
    s = swap(
        s,
        "Dependencies and why each exists: cpal (audio output), midir (MIDI input), eframe + egui (GUI), gilrs (gamepad input), rtrb (lock-free SPSC ring buffer), triple_buffer (lock-free latest-wins parameter sharing), basedrop (deferred deallocation for real-time), serde + serde_json (preset/session serialization), symphonia (audio file decoding).",
        f"Dependencies and why each exists: {DEPS_BY_PHASE[tier]}.",
        f"manifest deps p{n}",
    )
    if n == 1:
        s = swap(s, 'uses: ["domainService.Engine.EngineRenderer", "valueObject.Kernel.Frequency", "valueObject.Kernel.AudioFrame"]',
                 TONE_TEST_P1_USES, "tone_test uses p1")
        s = swap(s, '"Trigger a single A440 note through the engine, render one second of audio into a buffer, and MEASURE the peak absolute sample value of the rendered buffer.",',
                 TONE_TEST_P1_PROMPT, "tone_test prompt p1")
    return s


# ── Assembly ──────────────────────────────────────────────────────────────────

FILES_BY_PHASE = {
    1: {"kernel.cue": None, "shell.cue": shell_early},
    2: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None},
    3: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None, "realtime.cue": None},
    4: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None, "realtime.cue": None, "effects.cue": None},
    5: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None, "realtime.cue": None, "effects.cue": None, "mixer.cue": None},
    6: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None, "realtime.cue": None, "effects.cue": None, "mixer.cue": None, "sample.cue": sample_phase6},
    7: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None, "realtime.cue": None, "effects.cue": None, "mixer.cue": None, "sample.cue": None},
    8: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None, "realtime.cue": None, "effects.cue": None, "mixer.cue": None, "sample.cue": None, "modulation.cue": None, "patch.cue": None},
    9: {"kernel.cue": None, "shell.cue": shell_early, "engine.cue": None, "realtime.cue": None, "effects.cue": None, "mixer.cue": None, "sample.cue": None, "modulation.cue": None, "patch.cue": None, "preset.cue": preset_phase9},
    10: {"kernel.cue": None, "shell.cue": None, "engine.cue": None, "realtime.cue": None, "effects.cue": None, "mixer.cue": None, "sample.cue": None, "modulation.cue": None, "patch.cue": None, "preset.cue": None},
}


def main():
    if os.path.isdir(PHASES):
        shutil.rmtree(PHASES)
    for n in range(1, 11):
        d = os.path.join(PHASES, f"phase-{n}")
        os.makedirs(d)
        with open(os.path.join(d, "project.cue"), "w") as f:
            f.write(read("project.cue") if n == 10 else project_for_phase(n))
        with open(os.path.join(d, "manifest.cue"), "w") as f:
            f.write(read("manifest.cue") if n == 10 else manifest_for_phase(n))
        for name, variant in FILES_BY_PHASE[n].items():
            with open(os.path.join(d, name), "w") as f:
                f.write(read(name) if variant is None else variant())
        print(f"phase-{n}: {len(FILES_BY_PHASE[n]) + 2} files")
    print("done — phase-10 mirrors spec/ exactly")


if __name__ == "__main__":
    sys.exit(main())
