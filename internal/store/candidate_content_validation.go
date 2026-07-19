package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// ValidateCandidateRepositoryBytes rejects presentation output that must never
// become repository content. It only inspects content; callers must persist or
// materialize the original byte slice unchanged after validation succeeds.
func ValidateCandidateRepositoryBytes(path string, content []byte) error {
	kind := candidatePresentationMetadata(path, content)
	if kind == "" {
		return nil
	}
	return fmt.Errorf(
		"candidate content for %s contains presentation metadata (%s); submit only exact raw repository bytes",
		path,
		kind,
	)
}

func validateStoredCandidateRepositoryBytes(ctx context.Context, q *db.Queries, files []db.CandidateFile) error {
	for _, file := range files {
		blob, err := q.GetContentBlob(ctx, file.ContentBlob)
		if err != nil {
			return fmt.Errorf("content for %s: %w", file.Path, err)
		}
		if err := ValidateCandidateRepositoryBytes(file.Path, blob.Content); err != nil {
			return err
		}
	}
	return nil
}

func candidatePresentationMetadata(path string, content []byte) string {
	firstLine, remainder, hasNewline := candidateFirstLine(content)
	if hasNewline && isCandidateUpdateModeEnvelope(path, firstLine, remainder) {
		return "UPDATE MODE envelope"
	}
	if hasNewline && isCandidateAbsolutePathBanner(path, firstLine, remainder) {
		return "absolute-path formatter banner"
	}
	if hasNewline && isCandidateFileEnvelope(path, firstLine, remainder) {
		return "File envelope"
	}
	if isSourceLikeCandidate(path) && isCodeFenceOpening(firstLine) {
		return "code-fence envelope"
	}
	return ""
}

func isCandidateUpdateModeEnvelope(path, firstLine string, remainder []byte) bool {
	if !strings.HasPrefix(firstLine, "UPDATE MODE:") || !startsWithBlankLine(remainder) {
		return false
	}
	fileLine, framed, hasNewline := candidateFirstLine(consumeBlankLine(remainder))
	return hasNewline && isCandidateFileEnvelope(path, fileLine, framed)
}

func candidateFirstLine(content []byte) (line string, remainder []byte, hasNewline bool) {
	index := bytes.IndexByte(content, '\n')
	if index < 0 {
		return strings.TrimSuffix(string(content), "\r"), nil, false
	}
	return strings.TrimSuffix(string(content[:index]), "\r"), content[index+1:], true
}

func isCandidateAbsolutePathBanner(path, firstLine string, remainder []byte) bool {
	if !startsWithBlankLine(remainder) || !strings.HasSuffix(firstLine, ":") {
		return false
	}
	bannerPath := strings.TrimSuffix(firstLine, ":")
	if !isPortableAbsolutePath(bannerPath) {
		return false
	}
	return pathReferenceMatchesCandidate(bannerPath, path)
}

func isCandidateFileEnvelope(path, firstLine string, remainder []byte) bool {
	const prefix = "File: "
	if !strings.HasPrefix(firstLine, prefix) || !startsWithBlankLine(remainder) {
		return false
	}
	if !pathReferenceMatchesCandidate(strings.TrimSpace(strings.TrimPrefix(firstLine, prefix)), path) {
		return false
	}
	framed := consumeBlankLine(remainder)
	line, _, _ := candidateFirstLine(framed)
	return isCodeFenceOpening(line)
}

func pathReferenceMatchesCandidate(reference, candidate string) bool {
	reference = filepath.ToSlash(filepath.Clean(strings.ReplaceAll(reference, "\\", "/")))
	candidate = filepath.ToSlash(filepath.Clean(strings.ReplaceAll(candidate, "\\", "/")))
	if reference == candidate {
		return true
	}
	return isPortableAbsolutePath(reference) && strings.HasSuffix(reference, "/"+strings.TrimPrefix(candidate, "./"))
}

func isPortableAbsolutePath(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	if strings.HasPrefix(normalized, "/") {
		return true
	}
	return len(normalized) >= 3 && isASCIILetter(normalized[0]) && normalized[1] == ':' && normalized[2] == '/'
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func startsWithBlankLine(content []byte) bool {
	return bytes.HasPrefix(content, []byte("\n")) || bytes.HasPrefix(content, []byte("\r\n"))
}

func consumeBlankLine(content []byte) []byte {
	if bytes.HasPrefix(content, []byte("\r\n")) {
		return content[2:]
	}
	if bytes.HasPrefix(content, []byte("\n")) {
		return content[1:]
	}
	return content
}

func isCodeFenceOpening(line string) bool {
	if len(line) < 3 {
		return false
	}
	marker := line[:3]
	if marker != "```" && marker != "~~~" {
		return false
	}
	return !strings.Contains(line[3:], marker)
}

func isSourceLikeCandidate(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile", "makefile", "rakefile", "gemfile", "justfile":
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".bash", ".c", ".cc", ".cfg", ".clj", ".cljs", ".cljc", ".cpp", ".cs", ".css", ".cue",
		".cxx", ".dart", ".ex", ".exs", ".fish", ".fs", ".fsx", ".go", ".h", ".hcl", ".hpp", ".hrl",
		".html", ".java", ".js", ".json", ".jsx", ".kt", ".kts", ".lua", ".m", ".mm", ".nim", ".php",
		".proto", ".ps1", ".py", ".rb", ".rs", ".scala", ".scss", ".sh", ".sol", ".sql", ".swift",
		".tf", ".toml", ".ts", ".tsx", ".vb", ".xml", ".yaml", ".yml", ".zig", ".zsh":
		return true
	default:
		return false
	}
}
