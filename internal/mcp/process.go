package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type processTree interface {
	ParentProcess(pid int) (name string, ppid int, err error)
	SelfPID() int
}

type OSProcessTree struct{}

func (OSProcessTree) SelfPID() int {
	return os.Getpid()
}

func (OSProcessTree) ParentProcess(pid int) (string, int, error) {
	out, err := exec.Command("ps", "-o", "comm=,ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", 0, fmt.Errorf("ps for pid %d: %w", pid, err)
	}

	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", 0, fmt.Errorf("no output from ps for pid %d", pid)
	}

	idx := strings.LastIndexByte(line, ' ')
	if idx < 0 {
		return "", 0, fmt.Errorf("unexpected ps output format: %q", line)
	}

	name := strings.TrimSpace(line[:idx])
	ppidStr := strings.TrimSpace(line[idx+1:])
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil {
		return "", 0, fmt.Errorf("parse ppid %q: %w", ppidStr, err)
	}

	return name, ppid, nil
}
