package spec

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAllRepairWitnessesMustPassTheSameAcceptedTree(t *testing.T) {
	contract := cuepkg.VerificationContract{RepairWitnesses: map[string]cuepkg.Witness{
		"witness.left":  {ID: "witness.left"},
		"witness.right": {ID: "witness.right"},
	}}
	latest := map[string]store.LatestValidationResult{
		"witness.left":  {Classification: "passed", SourceTreeHash: "tree-current"},
		"witness.right": {Classification: "passed", SourceTreeHash: "tree-old"},
	}
	require.False(t, allRepairWitnessesPassed(contract, latest, "tree-current"),
		"mixed-tree evidence must not stamp a repair-owner contract")

	result := latest["witness.right"]
	result.SourceTreeHash = "tree-current"
	latest["witness.right"] = result
	require.True(t, allRepairWitnessesPassed(contract, latest, "tree-current"))

	result.Classification = "theater"
	latest["witness.right"] = result
	require.False(t, allRepairWitnessesPassed(contract, latest, "tree-current"))
}
