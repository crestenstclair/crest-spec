project: {
	name: "e2e-test"
	layers: ["domain", "application"]
	meta: {
		language: "go"
		style:    "idiomatic Go, DDD"
	}

	contexts: Counter: {
		purpose: "a simple counter bounded-context for end-to-end testing"
		ubiquitousLanguage: {
			counter: "A named counter that can be incremented and decremented"
			tally:   "The current value of a counter"
		}
		aggregates: Counter: {
			root:    true
			purpose: "Manages a named counter with min/max bounds"
			state: {
				name:  "string"
				value: "int"
				min:   "int"
				max:   "int"
			}
			commands: {
				Increment: {amount: "int"}
				Decrement: {amount: "int"}
				Reset:     {}
			}
			events: {
				Incremented: {name: "string", newValue: "int"}
				Decremented: {name: "string", newValue: "int"}
				WasReset:    {name: "string"}
			}
			invariants: [
				"value must be >= min",
				"value must be <= max",
				"amount must be positive for Increment and Decrement",
			]
		}
		valueObjects: CounterName: {
			state: value: "string"
			invariants: ["must not be empty", "must not exceed 64 characters"]
		}
		repositories: CounterStore: {
			of:       "aggregate.Counter.Counter"
			contract: {
				get:    "(name string) -> (Counter, error)"
				save:   "(Counter) -> error"
				delete: "(name string) -> error"
				list:   "() -> ([]Counter, error)"
			}
		}
		ports: Persistence: {
			contract: {
				load: "(key string) -> ([]byte, error)"
				save: "(key string, data []byte) -> error"
			}
		}
	}

	adapters: MemoryAdapter: {
		implements: "port.Counter.Persistence"
		layer:      "application"
	}

	assetKinds: source_file: {
		description: "Go source file"
		filePattern: "{{snakeCase .Name}}.go"
	}

	assets: README: {
		kind:        "source_file"
		description: "Project readme"
	}

	invariants: [{text: "All aggregates must have a root entity"}]
	contextMap: [{from: "Counter", to: "Counter", kind: "identity"}]
}
