package agentloop

import (
	"os"
	"sync"
)

// ProjectionCache memoizes decoded projections across sweeps.
//
// It exists because of a size mismatch the contract cannot fix on its own: the
// projection is capped at four kilobytes, but it is nested in a state document
// that carries the run's whole history and routinely reaches hundreds of
// kilobytes. Finding the `agent_mux` member means scanning the entire document,
// so an uncached sweep re-parses every byte every plan has ever accumulated.
// On a long-lived machine that is tens of megabytes per sweep — enough to blow
// the daemon's bounded startup budget outright, and to repeat that work every
// reconciliation interval for the daemon's whole life.
//
// The invalidation key is the state file's modification time and size. ASL
// publishes state by atomic rename, so any content change lands as a new inode
// with a fresh mtime, and these documents grow as the run progresses. A change
// could only be missed by a rewrite that both preserved the exact byte length
// and reused the same nanosecond timestamp; the next real transition would
// correct it. That is the trade being made deliberately, and it is bounded:
// the cache never turns absence into presence, it only defers noticing a
// change until the file changes again.
//
// A nil *ProjectionCache is valid and simply reads through, so tests and
// one-shot callers need not build one.
type ProjectionCache struct {
	mu      sync.Mutex
	entries map[string]projectionCacheEntry
}

type projectionCacheEntry struct {
	modTimeUnixNano int64
	size            int64
	projection      Projection
	err             error
}

// NewProjectionCache returns an empty cache ready for concurrent use.
func NewProjectionCache() *ProjectionCache {
	return &ProjectionCache{entries: make(map[string]projectionCacheEntry)}
}

// read returns the projection at path, reusing the previous decode when the
// file has not changed. Both outcomes are cached: a corrupt or unsupported
// document is the expensive case to re-read, and its verdict is just as stable
// as a valid one.
func (c *ProjectionCache) read(path string) (Projection, error) {
	if c == nil {
		return ReadProjection(path)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		// Without a stat there is nothing to validate a cached entry against, so
		// fall through to the live read and let it classify the failure.
		return ReadProjection(path)
	}
	modTime, size := info.ModTime().UnixNano(), info.Size()

	c.mu.Lock()
	entry, ok := c.entries[path]
	c.mu.Unlock()
	if ok && entry.modTimeUnixNano == modTime && entry.size == size {
		return entry.projection, entry.err
	}

	projection, err := ReadProjection(path)

	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]projectionCacheEntry)
	}
	c.entries[path] = projectionCacheEntry{
		modTimeUnixNano: modTime,
		size:            size,
		projection:      projection,
		err:             err,
	}
	c.mu.Unlock()
	return projection, err
}

// retain drops every entry whose path was not part of the sweep just finished,
// so a cache in a long-running daemon tracks the plans that still exist rather
// than every plan the machine has ever held.
func (c *ProjectionCache) retain(paths map[string]struct{}) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for path := range c.entries {
		if _, ok := paths[path]; !ok {
			delete(c.entries, path)
		}
	}
}

// Len reports how many decodes the cache is holding.
func (c *ProjectionCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
