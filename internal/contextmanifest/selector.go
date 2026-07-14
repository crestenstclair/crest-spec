package contextmanifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	SelectorVersion  = "context-selector-v1"
	EstimatorVersion = "chars-per-token-v1:4"
	DefaultBudget    = 32768
	MinimumBudget    = 1024
	MaximumBudget    = 131072
)

type Priority int

const (
	PriorityGoal       Priority = 10
	PriorityAcceptance Priority = 20
	PriorityTask       Priority = 30
	PriorityContract   Priority = 40
	PriorityDependency Priority = 50
	PriorityConsumer   Priority = 60
	PriorityCode       Priority = 70
	PriorityFailure    Priority = 80
	PriorityConvention Priority = 90
	PriorityBackground Priority = 100
)

type Decision string

const (
	Included  Decision = "included"
	Truncated Decision = "truncated"
	Omitted   Decision = "omitted"
)

type Candidate struct {
	Kind              string
	Title             string
	SourceKind        string
	SourceID          string
	SourcePath        string
	Content           string
	Priority          Priority
	Mandatory         bool
	Truncatable       bool
	InclusionReason   string
	UnavailableReason string
}

type Section struct {
	Ordinal         int      `json:"ordinal"`
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	SourceKind      string   `json:"source_kind"`
	SourceID        string   `json:"source_id"`
	SourcePath      string   `json:"source_path,omitempty"`
	Priority        Priority `json:"priority"`
	Mandatory       bool     `json:"mandatory"`
	Decision        Decision `json:"decision"`
	Reason          string   `json:"reason"`
	OriginalHash    string   `json:"original_hash"`
	SelectedHash    string   `json:"selected_hash,omitempty"`
	OriginalBytes   int      `json:"original_bytes"`
	SelectedBytes   int      `json:"selected_bytes"`
	EstimatedTokens int      `json:"estimated_tokens"`
	Content         string   `json:"content,omitempty"`
}

type Result struct {
	SelectorVersion  string    `json:"selector_version"`
	EstimatorVersion string    `json:"estimator_version"`
	BudgetTokens     int       `json:"budget_tokens"`
	EstimatedTokens  int       `json:"estimated_tokens"`
	ContextHash      string    `json:"context_hash"`
	Blocked          bool      `json:"blocked"`
	BlockedReason    string    `json:"blocked_reason,omitempty"`
	Sections         []Section `json:"sections"`
}

func NormalizeBudget(requested int) int {
	if requested == 0 {
		return DefaultBudget
	}
	if requested < MinimumBudget {
		return MinimumBudget
	}
	if requested > MaximumBudget {
		return MaximumBudget
	}
	return requested
}

// BudgetForRole provides host-independent defaults while still applying the
// engine's hard minimum and maximum to explicit host requests.
func BudgetForRole(role string, requested int) int {
	if requested != 0 {
		return NormalizeBudget(requested)
	}
	switch role {
	case "integration_implementer", "project_completion_reviewer":
		return 49152
	case "minimal_diff_repair", "failure_triage", "test_generator", "behavioral_witness_author":
		return 24576
	default:
		return DefaultBudget
	}
}

func EstimateTokens(content string) int {
	if content == "" {
		return 0
	}
	return (len([]byte(content)) + 3) / 4
}

func Hash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func Select(candidates []Candidate, requestedBudget int) Result {
	budget := NormalizeBudget(requestedBudget)
	ordered := canonicalCandidates(candidates)
	result := Result{
		SelectorVersion: SelectorVersion, EstimatorVersion: EstimatorVersion,
		BudgetTokens: budget, Sections: make([]Section, 0, len(ordered)),
	}

	mandatoryTokens := 0
	mandatoryContent := make(map[string]bool)
	mandatoryIdentities := make(map[string]bool)
	for _, candidate := range ordered {
		hash := Hash(candidate.Content)
		identity := candidate.Kind + "\x00" + candidate.SourceKind + "\x00" + candidate.SourceID
		if candidate.Mandatory && candidate.UnavailableReason == "" && !mandatoryContent[hash] && !mandatoryIdentities[identity] {
			mandatoryTokens += candidateTokenCost(candidate)
			mandatoryContent[hash] = true
			mandatoryIdentities[identity] = true
		}
	}
	if mandatoryTokens > budget {
		result.Blocked = true
		result.BlockedReason = fmt.Sprintf("mandatory context requires %d estimated tokens but budget is %d", mandatoryTokens, budget)
	}

	remaining := budget
	mandatoryRemaining := mandatoryTokens
	reservedMandatoryContent := make(map[string]bool, len(mandatoryContent))
	seen := make(map[string]bool, len(ordered))
	seenContent := make(map[string]string, len(ordered))
	for _, candidate := range ordered {
		section := baseSection(candidate, len(result.Sections))
		identity := candidate.Kind + "\x00" + candidate.SourceKind + "\x00" + candidate.SourceID
		contentHash := Hash(candidate.Content)
		if candidate.Mandatory && candidate.UnavailableReason == "" && mandatoryContent[contentHash] && !reservedMandatoryContent[contentHash] {
			mandatoryRemaining -= candidateTokenCost(candidate)
			reservedMandatoryContent[contentHash] = true
		}
		switch {
		case candidate.UnavailableReason != "":
			section.Decision = Omitted
			section.Reason = candidate.UnavailableReason
		case seen[identity]:
			section.Decision = Omitted
			section.Reason = "duplicate source identity"
		case seenContent[contentHash] != "":
			section.Decision = Omitted
			section.Reason = "duplicate content hash; first source is " + seenContent[contentHash]
		case result.Blocked:
			section.Decision = Omitted
			section.Reason = result.BlockedReason
		default:
			seen[identity] = true
			tokens := candidateTokenCost(candidate)
			available := remaining
			if !candidate.Mandatory {
				available -= mandatoryRemaining
				if available < 0 {
					available = 0
				}
			}
			if tokens <= available {
				include(&section, candidate.Content, Included, candidate.InclusionReason)
				remaining -= section.EstimatedTokens
				seenContent[contentHash] = candidate.SourceKind + ":" + candidate.SourceID
			} else if candidate.Truncatable && available > 24 {
				content, ok := truncate(candidate.Content, available-EstimateTokens("## "+candidate.Title+"\n\n"))
				if ok {
					include(&section, content, Truncated, fmt.Sprintf("%s; truncated to remaining budget", candidate.InclusionReason))
					remaining -= section.EstimatedTokens
					seenContent[contentHash] = candidate.SourceKind + ":" + candidate.SourceID
				} else {
					section.Decision = Omitted
					section.Reason = "insufficient remaining budget for a useful truncation"
				}
			} else {
				section.Decision = Omitted
				section.Reason = "context budget exhausted by higher-priority material"
			}
		}
		result.EstimatedTokens += section.EstimatedTokens
		result.Sections = append(result.Sections, section)
	}
	result.ContextHash = resultHash(result)
	return result
}

func Render(result Result) string {
	var sections []string
	for _, section := range result.Sections {
		if section.Decision != Included && section.Decision != Truncated {
			continue
		}
		sections = append(sections, "## "+section.Title+"\n\n"+section.Content)
	}
	return strings.Join(sections, "\n\n")
}

func canonicalCandidates(candidates []Candidate) []Candidate {
	ordered := append([]Candidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.SourceKind != right.SourceKind {
			return left.SourceKind < right.SourceKind
		}
		if left.SourceID != right.SourceID {
			return left.SourceID < right.SourceID
		}
		return left.Title < right.Title
	})
	return ordered
}

func baseSection(candidate Candidate, ordinal int) Section {
	return Section{
		Ordinal: ordinal, Kind: candidate.Kind, Title: candidate.Title,
		SourceKind: candidate.SourceKind, SourceID: candidate.SourceID,
		SourcePath: candidate.SourcePath, Priority: candidate.Priority,
		Mandatory: candidate.Mandatory, OriginalHash: Hash(candidate.Content),
		OriginalBytes: len([]byte(candidate.Content)),
	}
}

func include(section *Section, content string, decision Decision, reason string) {
	section.Decision = decision
	section.Reason = reason
	section.Content = content
	section.SelectedHash = Hash(content)
	section.SelectedBytes = len([]byte(content))
	section.EstimatedTokens = EstimateTokens("## " + section.Title + "\n\n" + content)
}

func candidateTokenCost(candidate Candidate) int {
	return EstimateTokens("## " + candidate.Title + "\n\n" + candidate.Content)
}

func truncate(content string, tokenBudget int) (string, bool) {
	originalBytes := []byte(content)
	marker := fmt.Sprintf("\n\n[truncated by crest-spec: original_sha256=%s omitted_bytes=%%d]", Hash(content))
	maxBytes := tokenBudget * 4
	if maxBytes <= len(marker)+32 {
		return "", false
	}
	prefixBytes := maxBytes - len(marker) - 16
	if prefixBytes >= len(originalBytes) {
		return content, true
	}
	for prefixBytes > 0 && (originalBytes[prefixBytes]&0xc0) == 0x80 {
		prefixBytes--
	}
	omitted := len(originalBytes) - prefixBytes
	return string(originalBytes[:prefixBytes]) + fmt.Sprintf(marker, omitted), true
}

func resultHash(result Result) string {
	type hashSection struct {
		Ordinal      int      `json:"ordinal"`
		Kind         string   `json:"kind"`
		Title        string   `json:"title"`
		SourceKind   string   `json:"source_kind"`
		SourceID     string   `json:"source_id"`
		SourcePath   string   `json:"source_path"`
		Priority     Priority `json:"priority"`
		Mandatory    bool     `json:"mandatory"`
		Decision     Decision `json:"decision"`
		Reason       string   `json:"reason"`
		OriginalHash string   `json:"original_hash"`
		SelectedHash string   `json:"selected_hash"`
	}
	payload := struct {
		Selector  string        `json:"selector"`
		Estimator string        `json:"estimator"`
		Budget    int           `json:"budget"`
		Blocked   bool          `json:"blocked"`
		Sections  []hashSection `json:"sections"`
	}{Selector: result.SelectorVersion, Estimator: result.EstimatorVersion, Budget: result.BudgetTokens, Blocked: result.Blocked}
	for _, section := range result.Sections {
		payload.Sections = append(payload.Sections, hashSection{
			Ordinal: section.Ordinal, Kind: section.Kind, Title: section.Title, SourceKind: section.SourceKind,
			SourceID: section.SourceID, SourcePath: section.SourcePath, Priority: section.Priority, Mandatory: section.Mandatory,
			Decision: section.Decision, Reason: section.Reason,
			OriginalHash: section.OriginalHash, SelectedHash: section.SelectedHash,
		})
	}
	encoded, _ := json.Marshal(payload)
	return Hash(string(encoded))
}
