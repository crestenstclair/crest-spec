package cue

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// BehavioralContract is the executable acceptance context that can change the
// implementation required from one resource even when that resource's local
// declaration is unchanged.
type BehavioralContract struct {
	Capabilities map[string]Capability `json:"capabilities,omitempty"`
	Witnesses    map[string]Witness    `json:"witnesses,omitempty"`
}

// VerificationContract is the narrow repair-owning contract that can require
// a resource row in an apply session. Broad observer witnesses and capability
// acceptance remain evidence definitions, but do not manufacture repair work.
type VerificationContract struct {
	Validations     []Validation       `json:"validations,omitempty"`
	RepairWitnesses map[string]Witness `json:"repair_witnesses,omitempty"`
}

func (contract VerificationContract) Empty() bool {
	return len(contract.Validations) == 0 && len(contract.RepairWitnesses) == 0
}

func (contract VerificationContract) Hash() string {
	if contract.Empty() {
		return ""
	}
	encoded, _ := json.Marshal(contract)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum)
}

func (contract BehavioralContract) Empty() bool {
	return len(contract.Capabilities) == 0 && len(contract.Witnesses) == 0
}

func (contract BehavioralContract) Hash() string {
	if contract.Empty() {
		return ""
	}
	encoded, _ := json.Marshal(contract)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum)
}

// BehavioralContractForResource returns only capability acceptance and named
// witnesses that are explicitly linked to the resource. Explicit witness
// resource links keep generation context bounded and make authorship intent
// auditable instead of inferring whole-project ownership.
func (reg *Registry) BehavioralContractForResource(resourceID string) BehavioralContract {
	contract := BehavioralContract{}
	for _, capabilityID := range reg.ResourceCapabilities[resourceID] {
		name := trimCanonicalPrefix(capabilityID, "capability.")
		capability, ok := reg.Project.Capabilities[name]
		if !ok || len(capability.Acceptance) == 0 {
			continue
		}
		if contract.Capabilities == nil {
			contract.Capabilities = make(map[string]Capability)
		}
		contract.Capabilities[capabilityID] = capability
	}

	witnessNames := make([]string, 0, len(reg.Project.Witnesses))
	for name := range reg.Project.Witnesses {
		witnessNames = append(witnessNames, name)
	}
	sort.Strings(witnessNames)
	for _, name := range witnessNames {
		witness := reg.Project.Witnesses[name]
		if !containsCanonicalID(witness.Resources, resourceID) {
			continue
		}
		if contract.Witnesses == nil {
			contract.Witnesses = make(map[string]Witness)
		}
		contract.Witnesses["witness."+name] = witness
	}
	return contract
}

// VerificationContractForResource returns only executable resource checks and
// witnesses that explicitly authorize this resource as a repair owner.
func (reg *Registry) VerificationContractForResource(resourceID string) VerificationContract {
	contract := VerificationContract{}
	if resource, ok := reg.Resources[resourceID]; ok && len(resource.Validations) > 0 {
		contract.Validations = append([]Validation(nil), resource.Validations...)
	}
	if resource, ok := reg.Resources[resourceID]; ok {
		for _, amendment := range ResourceAmendments(resource) {
			if amendment.Validation != nil {
				contract.Validations = append(contract.Validations, *amendment.Validation)
			}
		}
	}
	witnessNames := make([]string, 0, len(reg.Project.Witnesses))
	for name := range reg.Project.Witnesses {
		witnessNames = append(witnessNames, name)
	}
	sort.Strings(witnessNames)
	for _, name := range witnessNames {
		witness := reg.Project.Witnesses[name]
		if !containsCanonicalID(witness.RepairResources, resourceID) {
			continue
		}
		if contract.RepairWitnesses == nil {
			contract.RepairWitnesses = make(map[string]Witness)
		}
		contract.RepairWitnesses["witness."+name] = witness
	}
	return contract
}

func (reg *Registry) attachBehavioralContractHashes() {
	for resourceID, resource := range reg.Resources {
		resource.BehavioralContractHash = reg.BehavioralContractForResource(resourceID).Hash()
		resource.VerificationContractHash = reg.VerificationContractForResource(resourceID).Hash()
		reg.Resources[resourceID] = resource
	}
}

func trimCanonicalPrefix(id, prefix string) string {
	if len(id) >= len(prefix) && id[:len(prefix)] == prefix {
		return id[len(prefix):]
	}
	return id
}

func containsCanonicalID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
