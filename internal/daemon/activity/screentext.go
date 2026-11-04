package activity

import (
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

// ScreenTextCache memoizes one visible-screen observation per source pane for
// the lifetime of a single probe tick. It holds backend.Screen observations
// and reads them through a backend.Observer, so the probes that share it do
// not depend on which backend a pane lives in.
//
// It buys two things, and the second is the reason it exists:
//
//   - Fewer backend round-trips. A managed Claude pane used to be scraped twice
//     per tick — once by the context-limit probe and once by the idle stop
//     probe — and a Codex pane sitting on a bolt was scraped twice as well, by
//     the prompt classifier and then by the idle classifier.
//   - One frame per pane per tick. Both Claude probes now classify the SAME
//     capture, which closes a real race: the context-limit veto inside the idle
//     classifier used to be evaluated against a different frame than the one
//     that decided whether the context-limit banner was on screen, so a pane
//     could show the banner to one probe and an idle composer to the other.
//
// The cache is per tick, never per daemon: a capture is only ever reused inside
// one pass of the screen probe loop, so no probe ever acts on a frame
// older than the tick it is running in. Entry points that drive a single probe
// outside that loop build their own one-shot cache, which is the same
// guarantee for a shorter tick.
//
// Errors are memoized too. "One read per pane per tick" has to hold on the
// failure path as well, or a wedged pane would still be dialed once per probe;
// the probes all treat a read failure as "no evidence this tick" and simply try
// again on the next one.
//
// The cache does not judge an observation. Whether a screen is still evidence
// about the pane a probe meant to read is probes.CheckScreen's decision, made
// by the caller on every use.
//
// It is deliberately NOT safe for concurrent use: the probe loop is a single
// goroutine (see runScreenProbes) and a mutex here would only hide a future
// caller that broke that assumption.
//
// Only the visible screen is cached. The Codex clear probe's second
// scrollback read is a different capture and stays
// uncached, so the map can stay keyed on the pane alone.
type ScreenTextCache struct {
	observer backend.Observer
	entries  map[backend.Ref]screenEntry
	onRead   func(elapsed time.Duration, err error)
}

// OnRead has fn told of every read that reaches the backend: how long it
// took and whether it failed. A cached answer reports nothing.
func (c *ScreenTextCache) OnRead(fn func(elapsed time.Duration, err error)) {
	c.onRead = fn
}

type screenEntry struct {
	screen backend.Screen
	err    error
}

// NewScreenObservationCache builds the per-tick cache over any backend's
// screen observer.
func NewScreenObservationCache(observer backend.Observer) *ScreenTextCache {
	return &ScreenTextCache{observer: observer, entries: make(map[backend.Ref]screenEntry)}
}

// Observe returns this tick's observation of one pane's visible screen,
// reading it through to the backend exactly once.
func (c *ScreenTextCache) Observe(pane backend.Ref) (backend.Screen, error) {
	if entry, ok := c.entries[pane]; ok {
		return entry.screen, entry.err
	}
	started := time.Now()
	screen, err := c.observer.Observe(pane)
	if c.onRead != nil {
		c.onRead(time.Since(started), err)
	}
	c.entries[pane] = screenEntry{screen: screen, err: err}
	return screen, err
}
