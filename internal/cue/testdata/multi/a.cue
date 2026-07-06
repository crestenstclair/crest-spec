project: {
	name: "multi-test"
	layers: ["domain"]
	meta: language: "go"
	contexts: Alpha: {
		purpose: "First context"
		aggregates: Widget: {
			root: true
			purpose: "A widget"
			state: size: "int"
		}
	}
}
