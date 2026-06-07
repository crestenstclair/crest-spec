package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildDeepReviewPrompt_ContainsAllSections(t *testing.T) {
	prompt := BuildDeepReviewPrompt("func main() { fmt.Println(\"hello\") }")

	// SOLID principles
	assert.Contains(t, prompt, "SOLID Principles")
	assert.Contains(t, prompt, "SRP")
	assert.Contains(t, prompt, "OCP")
	assert.Contains(t, prompt, "LSP")
	assert.Contains(t, prompt, "ISP")
	assert.Contains(t, prompt, "DIP")

	// Dependency injection
	assert.Contains(t, prompt, "Dependency Injection")
	assert.Contains(t, prompt, "constructor injection")

	// Code smells
	assert.Contains(t, prompt, "Code Smells")
	assert.Contains(t, prompt, "Bloaters")
	assert.Contains(t, prompt, "Couplers")
	assert.Contains(t, prompt, "Dispensables")

	// Clean code
	assert.Contains(t, prompt, "Clean Code Rules")
	assert.Contains(t, prompt, "Naming")
	assert.Contains(t, prompt, "Functions")

	// Design patterns
	assert.Contains(t, prompt, "Design Pattern")
	assert.Contains(t, prompt, "Factory Method")

	// Output format
	assert.Contains(t, prompt, "Output Format")
	assert.Contains(t, prompt, `"passed"`)
	assert.Contains(t, prompt, `"findings"`)

	// Code included
	assert.Contains(t, prompt, "fmt.Println")
}

func TestBuildDeepReviewPrompt_EmptyCode(t *testing.T) {
	prompt := BuildDeepReviewPrompt("")

	// Should still contain all section headers
	assert.Contains(t, prompt, "SOLID Principles")
	assert.Contains(t, prompt, "Code to Review")
}
