package graph

import "encoding/json"

// guidanceKeys are declaration fields that steer HOW a resource is generated
// (prose for the LLM) without changing WHAT the resource is (its shape,
// contract, or constraints). They are stripped from the effective/exported
// hash so a prompt or description edit affects only the edited resource's
// code-generation identity instead of cascading through every dependent.
var guidanceKeys = map[string]bool{
	"prompts":       true,
	"description":   true,
	"notes":         true,
	"rationale":     true,
	"examples":      true,
	"references":    true,
	"style":         true,
	"avoid":         true,
	"purpose":       true,
	"reviewLevel":   true,
	"validations":   true,
	"validation":    true,
	"contributesTo": true,
}

var verificationOnlyKeys = map[string]bool{
	"validations":   true,
	"validation":    true,
	"contributesTo": true,
}

// legacyGuidanceKeysV2 is the exact exported-contract projection used before
// validation and traceability fields were separated from implementation API
// identity. It is retained only for read-only migration proofs.
var legacyGuidanceKeysV2 = map[string]bool{
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
// events, invariants, contracts, targets, and dependencies — are untouched;
// changing any of them still moves the hash and cascades. Validation
// declarations are evidence definitions and never form an exported API.
func StructuralJSON(decl any) []byte {
	return canonicalJSONWithout(decl, guidanceKeys)
}

// CodeGenerationJSON removes only verification definitions. Guidance remains
// because editing resource-local implementation guidance is a real local
// generation input, while validations are executed and reconciled separately.
func CodeGenerationJSON(decl any) []byte {
	return canonicalJSONWithout(decl, verificationOnlyKeys)
}

func legacyStructuralJSONV2(decl any) []byte {
	return canonicalJSONWithout(decl, legacyGuidanceKeysV2)
}

func canonicalJSONWithout(decl any, excluded map[string]bool) []byte {
	data, err := json.Marshal(decl)
	if err != nil {
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return data
	}
	out, err := json.Marshal(stripKeys(v, excluded))
	if err != nil {
		return data
	}
	return out
}

func stripGuidance(v any) any {
	return stripKeys(v, guidanceKeys)
}

func stripKeys(v any, excluded map[string]bool) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if excluded[k] {
				delete(t, k)
				continue
			}
			t[k] = stripKeys(val, excluded)
		}
		return t
	case []any:
		for i := range t {
			t[i] = stripKeys(t[i], excluded)
		}
		return t
	default:
		return v
	}
}
