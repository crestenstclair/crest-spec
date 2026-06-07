package spec

import (
	"regexp"
	"strings"
)

var errorPatterns = []*regexp.Regexp{
	// Go: ./internal/spec/session.go:42:5: undefined: foo
	regexp.MustCompile(`(?m)^\.?/?([^\s:]+\.go):(\d+):`),
	// Rust: error[E0433]: ... --> src/Synth/Voice.rs:42:5
	regexp.MustCompile(`(?m)--> ([^\s:]+\.rs):(\d+):`),
	// TypeScript: src/components/App.tsx(42,5): error TS2304
	regexp.MustCompile(`(?m)^([^\s(]+\.tsx?)\(\d+,\d+\):`),
	// GCC/Clang: src/main.c:42:5: error:
	regexp.MustCompile(`(?m)^([^\s:]+\.[ch](?:pp)?):(\d+):\d+:`),
	// Python: File "path/to/file.py", line 42
	regexp.MustCompile(`(?m)File "([^"]+\.py)", line \d+`),
}

func parseErrorFilePaths(output string) []string {
	seen := make(map[string]bool)
	var paths []string

	for _, pat := range errorPatterns {
		matches := pat.FindAllStringSubmatch(output, -1)
		for _, m := range matches {
			path := strings.TrimPrefix(m[1], "./")
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}

	return paths
}
