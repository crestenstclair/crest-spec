package prompt

import "strings"

// BuildDeepReviewPrompt constructs a comprehensive SOLID/DI/clean code review
// prompt. The caller passes the code to review; the returned prompt instructs
// the LLM to produce structured JSON output matching ReviewOutput.
func BuildDeepReviewPrompt(code string) string {
	var b strings.Builder
	b.WriteString("# Deep Code Review: SOLID, Clean Code & Refactoring Analysis\n\n")
	b.WriteString("Perform a comprehensive review of the following code.\n\n")

	writeSolidSection(&b)
	writeDISection(&b)
	writeCodeSmellsSection(&b)
	writeCleanCodeSection(&b)
	writeDesignPatternsSection(&b)
	writeOutputInstructions(&b)

	b.WriteString("## Code to Review\n\n")
	b.WriteString("```\n")
	b.WriteString(code)
	b.WriteString("\n```\n")

	return b.String()
}

func writeSolidSection(b *strings.Builder) {
	b.WriteString("## SOLID Principles\n\n")
	b.WriteString("Check each principle and report violations:\n\n")
	b.WriteString("- **SRP (Single Responsibility):** Does each class/module have one reason to change? ")
	b.WriteString("Look for mixed concerns, god classes, and methods doing unrelated work.\n")
	b.WriteString("- **OCP (Open/Closed):** Can new behavior be added without modifying existing code? ")
	b.WriteString("Look for switch statements that must be edited to add new types.\n")
	b.WriteString("- **LSP (Liskov Substitution):** Can implementations be swapped without breaking callers? ")
	b.WriteString("Look for subtypes that violate base contracts.\n")
	b.WriteString("- **ISP (Interface Segregation):** Are interfaces focused? ")
	b.WriteString("Look for fat interfaces forcing implementors to stub unused methods.\n")
	b.WriteString("- **DIP (Dependency Inversion):** Do high-level modules depend on abstractions? ")
	b.WriteString("Look for concrete class dependencies where interfaces should exist.\n\n")
}

func writeDISection(b *strings.Builder) {
	b.WriteString("## Dependency Injection\n\n")
	b.WriteString("Check for DI violations:\n\n")
	b.WriteString("- Classes that instantiate their own dependencies (hidden `new` in class body)\n")
	b.WriteString("- Missing constructor injection for collaborators\n")
	b.WriteString("- Dependencies that should be behind interfaces but are concrete\n")
	b.WriteString("- Redundant coupling: two paths to the same data that can diverge\n")
	b.WriteString("- Singleton overuse where DI would be more appropriate\n\n")
}

func writeCodeSmellsSection(b *strings.Builder) {
	b.WriteString("## Code Smells (refactoring.guru catalog)\n\n")
	b.WriteString("Check for smells in these categories:\n\n")

	b.WriteString("**Bloaters:** Long methods, large classes, primitive obsession, ")
	b.WriteString("long parameter lists (>3), data clumps.\n")
	b.WriteString("**OO Abusers:** Switch statements on type codes, temporary fields, ")
	b.WriteString("refused bequest, alternative classes with different interfaces.\n")
	b.WriteString("**Change Preventers:** Divergent change (one class modified for unrelated reasons), ")
	b.WriteString("shotgun surgery (one change touches many classes), parallel inheritance.\n")
	b.WriteString("**Dispensables:** Duplicate code, lazy classes, data-only classes without behavior, ")
	b.WriteString("dead code, speculative generality, excessive comments.\n")
	b.WriteString("**Couplers:** Feature envy, inappropriate intimacy, message chains, middle man.\n\n")
}

func writeCleanCodeSection(b *strings.Builder) {
	b.WriteString("## Clean Code Rules\n\n")
	b.WriteString("- **Naming:** Intent-revealing, pronounceable, searchable. ")
	b.WriteString("Nouns for classes, verbs for methods. Same concept = same word.\n")
	b.WriteString("- **Functions:** Small, do one thing, few arguments (0-2 ideal, 3 max). ")
	b.WriteString("No side effects. Command/query separation.\n")
	b.WriteString("- **Comments:** Only what the code cannot say. Flag comments that restate code.\n")
	b.WriteString("- **Error handling:** No swallowed errors, no returning null where exceptions fit.\n")
	b.WriteString("- **Dead code:** Unused variables, functions, imports, commented-out blocks.\n\n")
}

func writeDesignPatternsSection(b *strings.Builder) {
	b.WriteString("## Design Pattern Opportunities\n\n")
	b.WriteString("Identify code that would benefit from a design pattern:\n\n")
	b.WriteString("- Scattered `new` with type switches → Factory Method\n")
	b.WriteString("- Constructor with 5+ parameters → Builder\n")
	b.WriteString("- If-else chains checking handler type → Chain of Responsibility\n")
	b.WriteString("- Behavior changes based on state → State pattern\n")
	b.WriteString("- Deep inheritance (>2 levels) → prefer composition\n")
	b.WriteString("- Only suggest patterns that solve an actual problem in the code.\n\n")
}

func writeOutputInstructions(b *strings.Builder) {
	b.WriteString("## Output Format\n\n")
	b.WriteString("Respond in JSON format:\n")
	b.WriteString("```json\n")
	b.WriteString(`{`)
	b.WriteString("\n")
	b.WriteString(`  "passed": true,`)
	b.WriteString("\n")
	b.WriteString(`  "findings": [`)
	b.WriteString("\n")
	b.WriteString(`    {`)
	b.WriteString("\n")
	b.WriteString(`      "severity": "critical|major|minor",`)
	b.WriteString("\n")
	b.WriteString(`      "description": "Description of the issue",`)
	b.WriteString("\n")
	b.WriteString(`      "file": "filename",`)
	b.WriteString("\n")
	b.WriteString(`      "line": 0`)
	b.WriteString("\n")
	b.WriteString(`    }`)
	b.WriteString("\n")
	b.WriteString(`  ],`)
	b.WriteString("\n")
	b.WriteString(`  "summary": "Overall assessment"`)
	b.WriteString("\n")
	b.WriteString(`}`)
	b.WriteString("\n")
	b.WriteString("```\n\n")
	b.WriteString("Set `passed` to false if any critical or major findings exist. ")
	b.WriteString("Minor-only findings should still pass.\n\n")
}
