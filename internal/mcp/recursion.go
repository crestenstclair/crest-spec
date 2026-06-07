package mcp

import (
	"path/filepath"
	"strings"
)

func DetectRecursion(pt processTree) bool {
	pid := pt.SelfPID()
	visited := make(map[int]bool)
	claudeCount := 0

	for pid > 1 {
		if visited[pid] {
			break
		}
		visited[pid] = true

		name, ppid, err := pt.ParentProcess(pid)
		if err != nil {
			break
		}

		base := strings.ToLower(filepath.Base(name))
		if strings.Contains(base, "claude") &&
			!strings.Contains(base, "crest-spec") &&
			!strings.Contains(base, "mcp") {
			claudeCount++
		}

		if claudeCount > 1 {
			return true
		}

		pid = ppid
	}

	return false
}
