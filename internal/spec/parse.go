package spec

import (
	"fmt"
	"regexp"
	"strings"
)

type CodeBlock struct {
	Path    string
	Content string
	Lang    string
}

var (
	fenceOpenRe = regexp.MustCompile("^```(\\w*)\\s*$")
	pathRe      = regexp.MustCompile(`^(?://|#)\s*path:\s*(.+)$`)
)

func ParseCodeBlocks(output string) ([]CodeBlock, error) {
	lines := strings.Split(output, "\n")
	var blocks []CodeBlock
	var current *CodeBlock
	var contentLines []string

	for _, line := range lines {
		if current == nil {
			if m := fenceOpenRe.FindStringSubmatch(line); m != nil {
				current = &CodeBlock{Lang: m[1]}
				contentLines = nil
			}
			continue
		}

		if strings.TrimSpace(line) == "```" {
			current.Content = strings.Join(contentLines, "\n")
			blocks = append(blocks, *current)
			current = nil
			continue
		}

		if len(contentLines) == 0 {
			if m := pathRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				current.Path = strings.TrimSpace(m[1])
				continue
			}
		}

		contentLines = append(contentLines, line)
	}

	if len(blocks) == 0 {
		return nil, fmt.Errorf("no code blocks found in output")
	}

	return blocks, nil
}
