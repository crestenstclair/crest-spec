package graph

import "encoding/json"

// guidanceKeys are declaration fields that steer HOW a resource is generated
// (prose for the LLM) without changing WHAT the resource is (its shape,
// contract, or constraints). They are stripped from the effective hash so a
// prompt or description edit regenerates only the edited resource — the
// planner's declaration hash still covers the full declaration — instead of
// cascading a regeneration through every dependent.
var guidanceKeys = map[string]bool{
	"prompts":     true,
	"description": true,
	"notes":       true,
	"rationale":   true,
	"examples":    true,
	"references":  true,
	"style":       true,
	"avoid":       true,
	"purpose":     true,
	"reviewLevel": true,
}

// StructuralJSON returns the declaration's canonical JSON with guidance-only
// keys removed recursively. Structural fields — types, state, commands,
// events, invariants, contracts, targets, validations, dependencies — are
// untouched; changing any of them still moves the hash and cascades.
func StructuralJSON(decl any) []byte {
	data, err := json.Marshal(decl)
	if err != nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return data
	}
	out, err := json.Marshal(stripGuidance(v))
	if err != nil {
		return data
	}
	return out
}

func stripGuidance(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if guidanceKeys[k] {
				delete(t, k)
				continue
			}
			t[k] = stripGuidance(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = stripGuidance(t[i])
		}
		return t
	default:
		return v
	}
}
