# crest-spec CUE schema reference

Everything is under the top-level `project:`. All `.cue` files in the spec dir
share one package clause and are unified. Field names below mirror the Go loader
(`internal/cue/types.go`); fields not listed are ignored.

## project (top level)

| field | shape | notes |
|-------|-------|-------|
| `name` | string | project name |
| `layers` | `[string]` | architectural layers, e.g. `["domain","application","infrastructure"]` |
| `layerRules` | `{<layer>: {dependsOn: [string]}}` | allowed dependency direction; enforced as an invariant |
| `meta` | object | see **meta** |
| `contexts` | `{<Name>: Context}` | bounded contexts; see **context** |
| `adapters` | `{<Name>: Adapter}` | infrastructure adapters implementing a port |
| `assetKinds` | `{<kind>: AssetKind}` | reusable asset templates (description, filePattern, prompts) |
| `assets` | `{<Name>: Asset}` | concrete files to generate; see **asset** |
| `invariants` | `[Invariant]` or `{<group>: [Invariant]}` | global rules |
| `contextMap` | `[Rel]` or `{<name>: Rel}` | relationships between contexts |
| `validations` | `[Validation]` | **whole-tree gate**, run at wave verification |

## meta

`language`, `style`, `notes`, `rationale`, `framework`, `reviewLevel`, `mode`,
and string lists `rules`, `prompts`, `references`, `examples`, `avoid`. Use
`avoid` for crate-wide prohibitions (e.g. "heap allocation on audio thread").

## context (`project: contexts: <Name>:`)

| field | shape |
|-------|-------|
| `purpose` | string |
| `ubiquitousLanguage` | `{<Term>: "definition"}` |
| `meta` | object (same as project meta; per-context guidance) |
| `valueObjects` | `{<Name>: ValueObject}` |
| `aggregates` | `{<Name>: Aggregate}` |
| `entities` | `{<Name>: Entity}` |
| `ports` | `{<Name>: {contract: {...}, meta?: {...}}}` |
| `domainServices` | `{<Name>: {purpose: string, uses: [refs]}}` |
| `repositories` | `{<Name>: {...}}` |
| `applicationServices` | `{<Name>: {...}}` |
| `invariants` | `[Invariant]` or `{<group>: [Invariant]}` |

### valueObject
- Newtype: `{from: "u8", description: "...", invariants: ["must be 0-15"]}`
- Struct: `{state: {field: "Type", ...}, description: "...", invariants: [...]}`
- `enum`: `{from: "enum", description: "Idle, Attack, Decay, …"}`

### aggregate
```cue
{
	root: true
	purpose: "a single sounding note"
	state: {noteId: "NoteId", active: "bool", …}
	commands: [{name: "NoteOn", payload: {noteId: "NoteId", …}}]
	events:   [{name: "VoiceActivated", payload: {…}}]
	invariants: ["envelope progresses Idle -> Attack -> … -> Idle"]
}
```
`commands`/`events` accept the array-of-{name,payload} form OR a map form
`{NoteOn: {…}}` — both unmarshal.

### port
```cue
project: contexts: Synth: ports: SynthEngine: contract: {
	renderBlock: "(Voice, OscillatorConfig, FilterConfig) -> [AudioFrame]"
	noteOn:      "(Voice, NoteOn) -> Voice"
}
```
Contract values are signature strings. Add `meta: notes: "…"` for an
implementation hint (e.g. which crate).

## adapter (`project: adapters: <Name>:`)
```cue
project: adapters: CpalAudioOutput: {
	implements: "port.Shell.AudioOutput"
	layer:      "infrastructure"
	meta: notes: "cpal: cross-platform audio output"
}
```
The actual code comes from a paired `rust-adapter` asset whose `prompts` give the
file path and behavior. Co-locate adapter + its asset in the context's file.

## asset (`project: assets: <Name>:`)

| field | shape | notes |
|-------|-------|-------|
| `kind` | string | one of the `assetKinds` (e.g. `rust-bin-target`, `rust-adapter`, `cargo-manifest`, `makefile`, `rust-binary`) |
| `description` | string | include the target file path |
| `uses` | `[ref]` | resource refs this asset depends on, e.g. `"aggregate.Synth.Voice"`, `"port.Synth.SynthEngine"` — drives wave ordering |
| `prompts` | `[string]` | the generation brief; first line = file path |
| `validations` | `[Validation]` | per-asset proof |

## validation

```cue
{kind: "compiles", command: ["cargo", "build"], description: "crate builds"}
{kind: "test",     command: ["cargo", "test"],  description: "tests pass"}
{kind: "integration", command: ["make", "demo-voices"], description: "…",
	assertions: [
		{kind: "exit_code", expected: 0},
		{kind: "stdout_contains", pattern: "steals="},
		{kind: "file_exists", path: "voice-demo.wav"},
	]}
```
`kind`: `compiles` | `test` | `integration` (custom command). `assertions`
kinds: `exit_code` (`expected`), `stdout_contains` (`pattern`), `file_exists`
(`path`). Commands run in the project root.

## resource reference syntax (for `uses`)
`<kind>.<Context>.<Name>` — e.g. `aggregate.Synth.Voice`,
`valueObject.Kernel.MidiEvent`, `port.Shell.AudioOutput`,
`domainService.Synth.AudioRenderer`, `repository.Presets.PresetRepository`,
`asset.MidiFileLoader`, `adapter.CpalAudioOutput`.

## contextMap relationship
```cue
project: contextMap: kernelToSynth: {from: "Kernel", to: "Synth", kind: "shared-kernel"}
project: contextMap: shellToSynth:  {from: "Shell", to: "Synth", kind: "customer-supplier", direction: "downstream"}
```
`kind`: `shared-kernel` | `customer-supplier` | `anti-corruption` | … ;
optional `direction`.

## CUE gotchas
- Use raw strings `#"…"#` when a value contains `"` or backticks.
- Two concrete lists of different length will NOT unify — but with no phases each
  asset has one declaration, so this no longer arises. If you hit it, you have a
  duplicate declaration across files; remove one.
- Adding the same key with an identical value in two files is fine (unifies);
  conflicting concrete values are an error.
