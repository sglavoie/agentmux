package gitstate

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// countingGit answers every directory with a branch derived from its path and
// counts how many times each directory was probed, which is what makes a TTL
// hit distinguishable from a fresh read.
type countingGit struct {
	mu     sync.Mutex
	probes map[string]int
	before func(directory string)
}

func newCountingGit() *countingGit {
	return &countingGit{probes: make(map[string]int)}
}

func (g *countingGit) run(_ context.Context, directory string, _ time.Duration, args ...string) ([]byte, error) {
	if g.before != nil {
		g.before(directory)
	}
	g.mu.Lock()
	if args[0] == "rev-parse" {
		g.probes[directory]++
	}
	g.mu.Unlock()
	if args[0] == "rev-parse" {
		return []byte("branch-for" + directory + "\n"), nil
	}
	return nil, nil
}

func (g *countingGit) count(directory string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.probes[directory]
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestCacheServesWithinTTLAndReprobesAfterIt(t *testing.T) {
	git := newCountingGit()
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	cache := New(Options{Git: git.run, Now: clock.Now, TTL: 10 * time.Second})

	first := cache.Refresh(context.Background(), []string{"/repo/one"})
	if got := first["/repo/one"].Branch; got != "branch-for/repo/one" {
		t.Fatalf("first refresh branch = %q", got)
	}
	if got := git.count("/repo/one"); got != 1 {
		t.Fatalf("first refresh probed %d times, want 1", got)
	}

	clock.advance(9 * time.Second)
	second := cache.Refresh(context.Background(), []string{"/repo/one"})
	if got := git.count("/repo/one"); got != 1 {
		t.Fatalf("refresh inside the TTL probed %d times, want 1", got)
	}
	if second["/repo/one"] != first["/repo/one"] {
		t.Fatalf("refresh inside the TTL returned %+v, want the cached %+v", second["/repo/one"], first["/repo/one"])
	}

	clock.advance(2 * time.Second)
	cache.Refresh(context.Background(), []string{"/repo/one"})
	if got := git.count("/repo/one"); got != 2 {
		t.Fatalf("refresh past the TTL probed %d times, want 2", got)
	}
}

func TestCacheEvictsDirectoriesAbsentFromTheSnapshot(t *testing.T) {
	git := newCountingGit()
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	cache := New(Options{Git: git.run, Now: clock.Now})

	cache.Refresh(context.Background(), []string{"/repo/one", "/repo/two"})
	if got := cache.Len(); got != 2 {
		t.Fatalf("cache holds %d entries, want 2", got)
	}

	// The second directory is gone from the caller's snapshot: its entry must
	// go with it rather than linger until a TTL that nothing will consult.
	cache.Refresh(context.Background(), []string{"/repo/one"})
	if got := cache.Len(); got != 1 {
		t.Fatalf("cache holds %d entries after eviction, want 1", got)
	}

	// And the eviction is real, not a hidden retention: naming it again probes.
	cache.Refresh(context.Background(), []string{"/repo/one", "/repo/two"})
	if got := git.count("/repo/two"); got != 2 {
		t.Fatalf("re-added directory probed %d times, want 2", got)
	}
}

func TestCacheBoundsEntryCount(t *testing.T) {
	git := newCountingGit()
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	cache := New(Options{Git: git.run, Now: clock.Now, TTL: time.Hour, MaxEntries: 3})

	// Fill the cache one directory per refresh, a minute apart and with a TTL
	// nothing reaches, so every entry carries a distinct stamp. Eviction by
	// absence cannot help here — every directory stays wanted — so the entry
	// bound is the only thing that can act, and which entry it drops says
	// whether it acts by age.
	var all []string
	for i := 0; i < 3; i++ {
		all = append(all, fmt.Sprintf("/repo/%d", i))
		cache.Refresh(context.Background(), all)
		clock.advance(time.Minute)
	}
	if got := cache.Len(); got != 3 {
		t.Fatalf("cache holds %d entries before overflowing, want 3", got)
	}

	all = append(all, "/repo/3")
	cache.Refresh(context.Background(), all)
	if got := cache.Len(); got != 3 {
		t.Fatalf("cache holds %d entries, want the 3-entry bound", got)
	}
	if _, ok := cache.entries["/repo/0"]; ok {
		t.Fatal("cache kept /repo/0, the least recently probed entry")
	}
	for _, kept := range []string{"/repo/1", "/repo/2", "/repo/3"} {
		if _, ok := cache.entries[kept]; !ok {
			t.Fatalf("cache dropped %s, one of the three most recently probed entries", kept)
		}
	}

	// A wanted directory the bound evicted is simply probed again next time:
	// the bound trades a re-probe for a ceiling, it does not blacklist a row.
	cache.Refresh(context.Background(), all)
	if _, ok := cache.entries["/repo/0"]; !ok {
		t.Fatal("an evicted but still-wanted directory was not re-probed")
	}
}

func TestCacheNormalizesKeysAndDropsRelativeDirectories(t *testing.T) {
	git := newCountingGit()
	cache := New(Options{Git: git.run})

	states := cache.Refresh(context.Background(), []string{"/repo/one/", "/repo/./one", "", "  ", "relative/path"})
	if len(states) != 1 {
		t.Fatalf("refresh returned %d states, want 1: %+v", len(states), states)
	}
	if _, ok := states["/repo/one"]; !ok {
		t.Fatalf("refresh keyed its result on something other than the cleaned absolute path: %+v", states)
	}
	if got := git.count("/repo/one"); got != 1 {
		t.Fatalf("two spellings of one directory probed %d times, want 1", got)
	}
	if got := cache.Len(); got != 1 {
		t.Fatalf("cache holds %d entries, want 1", got)
	}
}

func TestKeyRejectsWhatItCannotResolve(t *testing.T) {
	if _, ok := Key("relative/path"); ok {
		t.Fatal("Key accepted a relative path; it would resolve against the daemon's working directory")
	}
	if _, ok := Key("   "); ok {
		t.Fatal("Key accepted a blank directory")
	}
	key, ok := Key("/repo/one/../one/")
	if !ok || key != "/repo/one" {
		t.Fatalf("Key() = %q, %v; want /repo/one, true", key, ok)
	}
}

// The cache's own mutex must not be held across a git invocation for the same
// reason its caller must not hold stateMu across one: a probe that waits out
// its full timeout would otherwise block every reader for that long.
func TestCacheHoldsNoLockAcrossAProbe(t *testing.T) {
	git := newCountingGit()
	cache := New(Options{Git: git.run})
	git.before = func(string) {
		if !cache.mu.TryLock() {
			t.Error("cache mutex was held across a git subprocess")
			return
		}
		cache.mu.Unlock()
	}
	cache.Refresh(context.Background(), []string{"/repo/one", "/repo/two", "/repo/three"})
}

func TestNilCacheRefreshesNothing(t *testing.T) {
	var cache *Cache
	if got := cache.Refresh(context.Background(), []string{"/repo/one"}); got != nil {
		t.Fatalf("nil cache returned %+v, want nil", got)
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("nil cache reported %d entries", got)
	}
}
