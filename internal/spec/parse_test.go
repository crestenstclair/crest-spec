package spec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCodeBlocks_SingleBlock(t *testing.T) {
	output := "Here is the code:\n\n```rust\n// path: src/Synth/Voice.rs\npub struct Voice {\n    frequency: f64,\n}\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "src/Synth/Voice.rs", blocks[0].Path)
	assert.Contains(t, blocks[0].Content, "pub struct Voice")
	assert.Equal(t, "rust", blocks[0].Lang)
}

func TestParseCodeBlocks_MultipleBlocks(t *testing.T) {
	output := "```go\n// path: src/Synth/Voice/voice.go\npackage voice\n```\n\n```go\n// path: src/Synth/Voice/voice_test.go\npackage voice_test\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "src/Synth/Voice/voice.go", blocks[0].Path)
	assert.Equal(t, "src/Synth/Voice/voice_test.go", blocks[1].Path)
}

func TestParseCodeBlocks_HashPathAnnotation(t *testing.T) {
	output := "```python\n# path: src/synth/voice.py\nclass Voice:\n    pass\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "src/synth/voice.py", blocks[0].Path)
}

func TestParseCodeBlocks_NoBlocks(t *testing.T) {
	output := "I'm sorry, I can't generate that code."

	blocks, err := ParseCodeBlocks(output)
	assert.Error(t, err)
	assert.Nil(t, blocks)
	assert.Contains(t, err.Error(), "no code blocks")
}

func TestParseCodeBlocks_BlockWithoutPath(t *testing.T) {
	output := "```rust\npub struct Voice {}\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "", blocks[0].Path)
}

func TestParseCodeBlocks_NoLeadingNewlineAfterPath(t *testing.T) {
	// Models commonly emit a blank line between the `// path:` annotation and the
	// first line of code. The extracted content must NOT begin with that blank
	// line, otherwise the written file starts with a newline and fails cargo fmt.
	output := "```rust\n// path: src/voice.rs\n\npub struct Voice {}\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "src/voice.rs", blocks[0].Path)
	assert.False(t, strings.HasPrefix(blocks[0].Content, "\n"), "content must not start with a newline")
	assert.True(t, strings.HasPrefix(blocks[0].Content, "pub struct Voice"), "content should start at the first real line")
}

func TestParseCodeBlocks_LeadingBlankLinesNoPath(t *testing.T) {
	// Leading blank lines with no path annotation are also trimmed from the start.
	output := "```rust\n\n\npub fn main() {}\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.True(t, strings.HasPrefix(blocks[0].Content, "pub fn main"), "leading blank lines should be dropped")
}

func TestParseCodeBlocks_MixedWithAndWithoutPath(t *testing.T) {
	output := "Some text\n```rust\n// path: src/voice.rs\ncode1\n```\nMore text\n```\ncode2\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "src/voice.rs", blocks[0].Path)
	assert.Equal(t, "", blocks[1].Path)
}
