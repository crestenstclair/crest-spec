package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatLearned_EmptyWhenNoRealContent(t *testing.T) {
	require.Equal(t, "", formatLearned("<!-- x -->"))
	require.Equal(t, "", formatLearned("  \n  "))
	require.Equal(t, "", formatLearned("<!-- multi\nline\ncomment -->"))
}

func TestFormatLearned_WithContent(t *testing.T) {
	got := formatLearned("- prefer blocking send")
	require.True(t, strings.Contains(got, "# Learned Practices"), "want header, got: %q", got)
	require.True(t, strings.Contains(got, "prefer blocking send"), "want body, got: %q", got)
}

func TestRenderLearned_RustPlaceholderIsEmpty(t *testing.T) {
	require.Equal(t, "", renderLearned("rust"))
}

func TestRenderLearned_MissingLangNoPanic(t *testing.T) {
	require.Equal(t, "", renderLearned("nonexistent-lang"))
}
