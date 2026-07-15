package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
)

// Coordinator serializes governed operations whose candidate path sets
// overlap while allowing independent vertical slices to proceed concurrently.
type Coordinator struct {
	mu      sync.Mutex
	active  map[string]struct{}
	changed chan struct{}
}

func NewCoordinator() *Coordinator {
	return &Coordinator{
		active:  make(map[string]struct{}),
		changed: make(chan struct{}),
	}
}

// Acquire reserves paths until the returned release function is called.
func (c *Coordinator) Acquire(ctx context.Context, paths []string) (release func(), err error) {
	if ctx == nil {
		return nil, fmt.Errorf("workspace coordination context is required")
	}
	keys := coordinationKeys(paths)
	if len(keys) == 0 {
		return func() {}, nil
	}

	for {
		c.mu.Lock()
		if c.available(keys) {
			for _, key := range keys {
				c.active[key] = struct{}{}
			}
			c.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() { c.release(keys) })
			}, nil
		}
		changed := c.changed
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("coordinate workspace paths: %w", ctx.Err())
		case <-changed:
		}
	}
}

func (c *Coordinator) available(keys []string) bool {
	for _, key := range keys {
		if _, exists := c.active[key]; exists {
			return false
		}
	}
	return true
}

func (c *Coordinator) release(keys []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range keys {
		delete(c.active, key)
	}
	close(c.changed)
	c.changed = make(chan struct{})
}

func coordinationKeys(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		seen[filepath.ToSlash(filepath.Clean(path))] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
