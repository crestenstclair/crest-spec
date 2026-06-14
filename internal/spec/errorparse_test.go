package spec

import (
	"testing"
)

func TestParseErrorFilePaths(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "go compiler error",
			input:    "./internal/spec/session.go:42:5: undefined: foo\n./internal/spec/query.go:10:2: imported and not used",
			expected: []string{"internal/spec/session.go", "internal/spec/query.go"},
		},
		{
			name:     "rust compiler error",
			input:    "error[E0433]: failed to resolve\n --> src/Synth/Voice.rs:42:5\n  |\n42 |     use crate::missing;\n  |         ^^^^^^^ not found",
			expected: []string{"src/Synth/Voice.rs"},
		},
		{
			name:     "typescript error",
			input:    "src/components/App.tsx(42,5): error TS2304: Cannot find name 'foo'\nsrc/utils/helpers.ts(10,1): error TS2322: Type mismatch",
			expected: []string{"src/components/App.tsx", "src/utils/helpers.ts"},
		},
		{
			name:     "python error",
			input:    "Traceback (most recent call last):\n  File \"app/models/user.py\", line 42, in create\n    raise ValueError",
			expected: []string{"app/models/user.py"},
		},
		{
			name:     "mixed errors",
			input:    "./cmd/main.go:10:5: syntax error\nerror[E0001]: --> src/lib.rs:5:1",
			expected: []string{"cmd/main.go", "src/lib.rs"},
		},
		{
			name:     "no errors",
			input:    "all tests passed",
			expected: nil,
		},
		{
			name:     "deduplicates paths",
			input:    "./internal/spec/session.go:42:5: error1\n./internal/spec/session.go:50:3: error2",
			expected: []string{"internal/spec/session.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseErrorFilePaths(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("got %d paths %v, want %d paths %v", len(got), got, len(tt.expected), tt.expected)
				return
			}
			for i, path := range got {
				if path != tt.expected[i] {
					t.Errorf("path[%d] = %q, want %q", i, path, tt.expected[i])
				}
			}
		})
	}
}
