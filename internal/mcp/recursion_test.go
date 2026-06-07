package mcp

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeProcess struct {
	name string
	ppid int
}

type fakeProcessTree struct {
	selfPID   int
	processes map[int]fakeProcess
}

func (f *fakeProcessTree) SelfPID() int {
	return f.selfPID
}

func (f *fakeProcessTree) ParentProcess(pid int) (string, int, error) {
	p, ok := f.processes[pid]
	if !ok {
		return "", 0, fmt.Errorf("process %d not found", pid)
	}
	return p.name, p.ppid, nil
}

func TestDetectRecursion_NoRecursion(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 50},
			50:  {name: "claude", ppid: 10},
			10:  {name: "zsh", ppid: 1},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_RecursionDetected(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 80},
			80:  {name: "node", ppid: 70},
			70:  {name: "claude", ppid: 60},
			60:  {name: "zsh", ppid: 1},
		},
	}

	assert.True(t, DetectRecursion(pt))
}

func TestDetectRecursion_CrestSpecAncestorNotCounted(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 80},
			80:  {name: "crest-spec", ppid: 70},
			70:  {name: "zsh", ppid: 1},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_MCPAncestorNotCounted(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 80},
			80:  {name: "claude-mcp-bridge", ppid: 70},
			70:  {name: "zsh", ppid: 1},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_SelfReferentialPID(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 90},
			90:  {name: "claude", ppid: 90},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_ProcessNotFound(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "crest-spec", ppid: 99},
		},
	}

	assert.False(t, DetectRecursion(pt))
}

func TestDetectRecursion_FullPathNames(t *testing.T) {
	pt := &fakeProcessTree{
		selfPID: 100,
		processes: map[int]fakeProcess{
			100: {name: "/usr/local/bin/crest-spec", ppid: 90},
			90:  {name: "/usr/local/bin/claude", ppid: 80},
			80:  {name: "/usr/local/bin/claude", ppid: 70},
			70:  {name: "/bin/zsh", ppid: 1},
		},
	}

	assert.True(t, DetectRecursion(pt))
}
