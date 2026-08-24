package daemon

import (
	"github.com/sglavoie/agentmux/internal/kitty"
)

// screenTextCache memoizes one `kitten @ get-text --extent=screen` capture per
// source pane for the lifetime of a single probe tick.
//
// It buys two things, and the second is the reason it exists:
//
//   - Fewer Kitty round-trips. A managed Claude pane used to be scraped twice
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
// one pass of runScreenProbesWithSnapshot, so no probe ever acts on a frame
// older than the tick it is running in. Entry points that drive a single probe
// outside that loop build their own one-shot cache, which is the same
// guarantee for a shorter tick.
//
// Errors are memoized too. "One read per pane per tick" has to hold on the
// failure path as well, or a wedged pane would still be dialed once per probe;
// the probes all treat a read failure as "no evidence this tick" and simply try
// again on the next one.
//
// It is deliberately NOT safe for concurrent use: the probe loop is a single
// goroutine (see runScreenProbes) and a mutex here would only hide a future
// caller that broke that assumption.
//
// Only the `screen` extent is cached. The Codex clear probe's second
// `--extent=all` read is a different capture of a different size and stays
// uncached, so the map can stay keyed on the window ID alone.
type screenTextCache struct {
	client  kittyTitleClient
	entries map[int]screenTextEntry
}

type screenTextEntry struct {
	screen string
	err    error
}

// newScreenTextCache builds the per-tick cache over the handler's Kitty client.
func newScreenTextCache(client kittyTitleClient) *screenTextCache {
	return &screenTextCache{client: client, entries: make(map[int]screenTextEntry)}
}

// screen returns this tick's `--extent=screen` capture of one pane, reading it
// through to Kitty exactly once.
func (c *screenTextCache) screen(sourceWindowID int) (string, error) {
	if entry, ok := c.entries[sourceWindowID]; ok {
		return entry.screen, entry.err
	}
	screen, err := c.client.GetText(kitty.MatchWindowByID(sourceWindowID), "screen")
	c.entries[sourceWindowID] = screenTextEntry{screen: screen, err: err}
	return screen, err
}

// screenTextCacheFor builds a one-shot cache for an entry point that drives a
// single probe outside the shared tick, so those paths keep the same "one read
// per pane" guarantee without reaching for a nil check at every call site.
func (h *Handler) screenTextCacheFor() *screenTextCache {
	return newScreenTextCache(h.kittyClient)
}
