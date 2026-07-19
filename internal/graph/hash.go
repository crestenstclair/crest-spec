package graph

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
)

// ComputeGenerationInputHashes returns the v3 code-generation inputs. An
// executable acceptance predicate or witness is verification work, not an
// instruction to rewrite otherwise-current code, so behavioral contracts have
// their own fingerprint below. Project-wide and inherited prompt changes stay
// in the operation identity, but the planner routes their mismatch through one
// affected-closure validation rather than model generation.
func ComputeGenerationInputHashes(resources map[string]cuepkg.Resource, project *cuepkg.Project) map[string]string {
	projectGeneratorContract := computeProjectGeneratorContract(project)
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		payload, _ := json.Marshal(struct {
			CodeDeclarationHash       string `json:"code_declaration_hash"`
			ProjectGeneratorContract  string `json:"project_generator_contract"`
			EffectiveResourceMetaHash string `json:"effective_resource_meta_hash"`
		}{
			CodeDeclarationHash:       hashBytes(CodeGenerationJSON(resource.Declaration)),
			ProjectGeneratorContract:  projectGeneratorContract,
			EffectiveResourceMetaHash: hashBytes(CodeGenerationJSON(resource.Meta)),
		})
		hashes[id] = hashBytes(payload)
	}
	return hashes
}

// ComputeCodeDeclarationHashes tracks the implementation-bearing declaration
// independently from the full declaration hash retained for provenance.
func ComputeCodeDeclarationHashes(resources map[string]cuepkg.Resource) map[string]string {
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		hashes[id] = hashBytes(CodeGenerationJSON(resource.Declaration))
	}
	return hashes
}

// ComputeDeclarationHashes retains the exact full declaration identity for
// provenance. Unlike the code declaration hash, verification and traceability
// fields intentionally remain present.
func ComputeDeclarationHashes(resources map[string]cuepkg.Resource) map[string]string {
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		encoded, _ := json.Marshal(resource.Declaration)
		hashes[id] = hashBytes(encoded)
	}
	return hashes
}

// ComputeLegacyGenerationInputHashesV2 exactly reproduces the behavior-
// inclusive fingerprint written before verification inputs were separated
// from code-generation inputs. An exact current v2 match is the approved
// migration baseline for stamping the split v3/verification projections
// without scheduling model work.
func ComputeLegacyGenerationInputHashesV2(resources map[string]cuepkg.Resource, project *cuepkg.Project) map[string]string {
	projectGeneratorContract := computeProjectGeneratorContract(project)
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		payload, _ := json.Marshal(struct {
			Declaration              any    `json:"declaration"`
			BehavioralContractHash   string `json:"behavioral_contract_hash"`
			ProjectGeneratorContract string `json:"project_generator_contract"`
		}{
			Declaration:              resource.Declaration,
			BehavioralContractHash:   resource.BehavioralContractHash,
			ProjectGeneratorContract: projectGeneratorContract,
		})
		hashes[id] = hashBytes(payload)
	}
	return hashes
}

// ComputeVerificationContractHashes returns only explicit repair-owner
// contracts. Empty means the resource has no scheduler-owning validation or
// repair witness; broad observers and capability acceptance stay in evidence
// reconciliation and never create resource work.
func ComputeVerificationContractHashes(resources map[string]cuepkg.Resource) map[string]string {
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		hashes[id] = resource.VerificationContractHash
	}
	return hashes
}

func computeProjectGeneratorContract(project *cuepkg.Project) string {
	if project == nil {
		return ""
	}
	// Hash the assembled prompt rather than duplicating its field selection
	// here. Mission, language-specific guidance, style, rules, avoid lists,
	// and architectural invariants stay aligned with what a worker receives.
	return hashBytes([]byte(promptpkg.BuildSystemPrompt(project)))
}

// ComputeLegacyGenerationInputHashesV1 reproduces the generation fingerprint
// written by migration 027 before the project-wide BuildSystemPrompt contract
// became part of the operation identity. It exists only as a version bridge:
// an exact match may be closure-validated, but must never be adopted directly
// as the current fingerprint.
func ComputeLegacyGenerationInputHashesV1(resources map[string]cuepkg.Resource) map[string]string {
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		payload, _ := json.Marshal(struct {
			Declaration            any    `json:"declaration"`
			BehavioralContractHash string `json:"behavioral_contract_hash"`
		}{
			Declaration:            resource.Declaration,
			BehavioralContractHash: resource.BehavioralContractHash,
		})
		hashes[id] = hashBytes(payload)
	}
	return hashes
}

// ComputeExportedContractHashes returns only the structural contract exposed
// to direct dependents. Guidance and behavioral witnesses can require local
// regeneration, but do not change the dependency-facing API and therefore do
// not fan out through the graph.
func ComputeExportedContractHashes(resources map[string]cuepkg.Resource) map[string]string {
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		hashes[id] = hashBytes(StructuralJSON(resource.Declaration))
	}
	return hashes
}

// ComputeDependencyContractHashes fingerprints the exact direct dependency
// contracts against which each resource must remain compatible. Dependency
// identity and edge kind are included so rewiring an otherwise identical API
// cannot be mistaken for a cache hit. The empty dependency set also receives
// a stable non-empty hash; an empty persisted value always means unknown.
func ComputeDependencyContractHashes(
	resources map[string]cuepkg.Resource,
	exportedContractHashes map[string]string,
) map[string]string {
	hashes := make(map[string]string, len(resources))
	for id, resource := range resources {
		type dependencyContract struct {
			TargetID     string `json:"target_id"`
			Kind         string `json:"kind"`
			ContractHash string `json:"contract_hash"`
		}
		contracts := make([]dependencyContract, 0, len(resource.Dependencies))
		for _, dependency := range resource.Dependencies {
			contracts = append(contracts, dependencyContract{
				TargetID:     dependency.TargetID,
				Kind:         dependency.Kind,
				ContractHash: exportedContractHashes[dependency.TargetID],
			})
		}
		sort.Slice(contracts, func(i, j int) bool {
			if contracts[i].TargetID != contracts[j].TargetID {
				return contracts[i].TargetID < contracts[j].TargetID
			}
			return contracts[i].Kind < contracts[j].Kind
		})
		encoded, _ := json.Marshal(contracts)
		hashes[id] = hashBytes(encoded)
	}
	return hashes
}

// ComputeLegacyDependencyContractHashesV2 exactly reproduces dependency
// fingerprints written before validations and contributesTo were removed from
// exported contracts. It is never a current planning hash; it only proves that
// an accepted operation differs because the hashing projection was upgraded.
func ComputeLegacyDependencyContractHashesV2(resources map[string]cuepkg.Resource) map[string]string {
	exported := make(map[string]string, len(resources))
	for id, resource := range resources {
		exported[id] = hashBytes(legacyStructuralJSONV2(resource.Declaration))
	}
	return ComputeDependencyContractHashes(resources, exported)
}

func hashBytes(value []byte) string {
	hash := sha256.Sum256(value)
	return fmt.Sprintf("%x", hash)
}

func ComputeEffectiveHashes(
	resources map[string]cuepkg.Resource,
	g *Graph,
	model string,
	mode string,
) map[string]string {
	order, err := g.TopologicalSort()
	if err != nil {
		order = make([]string, 0, len(resources))
		for id := range resources {
			order = append(order, id)
		}
		sort.Strings(order)
	}

	hashes := make(map[string]string, len(resources))

	for _, id := range order {
		r, ok := resources[id]
		if !ok {
			continue
		}

		// Structural JSON only: guidance edits (prompts, descriptions) must
		// not cascade regeneration through dependents. The planner's separate
		// declaration hash still catches them for the edited resource itself.
		declJSON := StructuralJSON(r.Declaration)

		var depHashes []string
		for _, e := range r.Dependencies {
			if h, ok := hashes[e.TargetID]; ok {
				depHashes = append(depHashes, h)
			}
		}
		sort.Strings(depHashes)

		h := sha256.New()
		h.Write(declJSON)
		// Capability acceptance and explicitly linked executable witnesses are
		// generation inputs, not documentation. A changed behavioral contract
		// must invalidate the resource that is responsible for satisfying it.
		h.Write([]byte(r.BehavioralContractHash))
		for _, dh := range depHashes {
			h.Write([]byte(dh))
		}
		h.Write([]byte(model))
		h.Write([]byte(mode))

		hashes[id] = fmt.Sprintf("%x", h.Sum(nil))
	}

	return hashes
}
