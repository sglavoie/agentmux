package gitstate

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTTL is how long a probed directory's answer is reused. A branch
	// name and a dirty bit change on human timescales, and the caller's sweep
	// runs once a second, so anything shorter would spend subprocesses to
	// re-learn what it already knows.
	DefaultTTL = 10 * time.Second

	// DefaultMaxEntries bounds the cache regardless of what the caller asks
	// for. Eviction by absence already tracks the caller's snapshot; this is
	// the backstop that keeps a caller who never shrinks its directory set from
	// growing the map without limit.
	DefaultMaxEntries = 256

	// DefaultWorkers bounds concurrent probes. It matches worktree's
	// maxInventoryWorkers: enough that a dozen directories do not serialize
	// into a dozen timeouts, few enough that a sweep cannot fork a process per
	// row.
	DefaultWorkers = 4
)

// Options configures a Cache. Every zero field takes the documented default, so
// New(Options{}) is the production configuration.
type Options struct {
	// Git is the command runner. Nil installs the real one.
	Git Executor
	// Now is the clock the TTL is measured against. Nil installs time.Now.
	Now func() time.Time
	// TTL, Timeout, MaxEntries and Workers override the Default* constants.
	TTL        time.Duration
	Timeout    time.Duration
	MaxEntries int
	Workers    int
}

type entry struct {
	state    State
	probedAt time.Time
}

// Cache is a bounded TTL cache of per-directory Git state, keyed by absolute
// directory.
//
// Its own mutex is NEVER held across a git invocation. Refresh runs in three
// steps — decide under the lock, probe off it, write back under it — for the
// same reason its caller must not hold the daemon's stateMu across a
// subprocess: a probe that blocks for its full timeout would otherwise block
// every reader of the cache for that long.
type Cache struct {
	git        Executor
	now        func() time.Time
	ttl        time.Duration
	timeout    time.Duration
	maxEntries int
	workers    int

	mu      sync.Mutex
	entries map[string]entry
}

// New constructs a cache. It performs no I/O and starts no goroutine; a cache
// only runs git while a Refresh call is in flight.
func New(opts Options) *Cache {
	cache := &Cache{
		git:        opts.Git,
		now:        opts.Now,
		ttl:        opts.TTL,
		timeout:    opts.Timeout,
		maxEntries: opts.MaxEntries,
		workers:    opts.Workers,
		entries:    make(map[string]entry),
	}
	if cache.git == nil {
		cache.git = runGit
	}
	if cache.now == nil {
		cache.now = time.Now
	}
	if cache.ttl <= 0 {
		cache.ttl = DefaultTTL
	}
	if cache.timeout <= 0 {
		cache.timeout = ProbeTimeout
	}
	if cache.maxEntries <= 0 {
		cache.maxEntries = DefaultMaxEntries
	}
	if cache.workers <= 0 {
		cache.workers = DefaultWorkers
	}
	return cache
}

// Refresh returns the state of every directory in directories, probing only
// those whose entry is missing or older than the TTL, and evicting every
// retained entry for a directory the caller no longer names. The returned map
// is keyed by the normalized absolute directory, which is what Key reports.
//
// Directories that are empty or relative are dropped rather than resolved: a
// relative path would resolve against the daemon's working directory, which is
// never the directory the caller meant.
func (c *Cache) Refresh(ctx context.Context, directories []string) map[string]State {
	if c == nil {
		return nil
	}
	wanted := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		if key, ok := Key(directory); ok {
			wanted[key] = struct{}{}
		}
	}

	// Step 1, under the lock: retire what the caller no longer names and decide
	// what is due. No subprocess runs here.
	result := make(map[string]State, len(wanted))
	var due []string
	now := c.now()
	c.mu.Lock()
	for key := range c.entries {
		if _, ok := wanted[key]; !ok {
			delete(c.entries, key)
		}
	}
	for key := range wanted {
		if cached, ok := c.entries[key]; ok && now.Sub(cached.probedAt) < c.ttl {
			result[key] = cached.state
			continue
		}
		due = append(due, key)
	}
	c.mu.Unlock()

	// Step 2, off the lock: the subprocesses. Sorted so a probe order is
	// reproducible rather than map-iteration order.
	sort.Strings(due)
	probed := c.probeAll(ctx, due)

	// Step 3, under the lock again: write back and re-bound the map.
	stamp := c.now()
	c.mu.Lock()
	for i, key := range due {
		c.entries[key] = entry{state: probed[i], probedAt: stamp}
		result[key] = probed[i]
	}
	c.evictOverflowLocked()
	c.mu.Unlock()
	return result
}

// Len reports how many directories the cache retains.
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// probeAll probes every directory concurrently, bounded by the worker count.
// Results are positional so no shared map needs a lock.
func (c *Cache) probeAll(ctx context.Context, directories []string) []State {
	if len(directories) == 0 {
		return nil
	}
	states := make([]State, len(directories))
	workers := c.workers
	if workers > len(directories) {
		workers = len(directories)
	}
	indexes := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indexes {
				states[i] = Probe(ctx, c.git, directories[i], c.timeout)
			}
		}()
	}
	for i := range directories {
		indexes <- i
	}
	close(indexes)
	wg.Wait()
	return states
}

// evictOverflowLocked drops the least recently probed entries until the map is
// within its bound. Ties break on the key so eviction is deterministic.
func (c *Cache) evictOverflowLocked() {
	if len(c.entries) <= c.maxEntries {
		return
	}
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := c.entries[keys[i]], c.entries[keys[j]]
		if left.probedAt.Equal(right.probedAt) {
			return keys[i] < keys[j]
		}
		return left.probedAt.Before(right.probedAt)
	})
	for _, key := range keys[:len(c.entries)-c.maxEntries] {
		delete(c.entries, key)
	}
}

// Key normalizes a directory into the cache key it would be stored under, and
// reports whether it is usable at all. Callers that need to correlate their own
// records with a Refresh result use it so both sides agree on one spelling.
func Key(directory string) (string, bool) {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" || !filepath.IsAbs(trimmed) {
		return "", false
	}
	return filepath.Clean(trimmed), true
}
