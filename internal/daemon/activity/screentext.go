package activity

import (
	"strconv"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
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
// The cache does not judge an observation. Whether a screen is still evidence
// about the pane a probe meant to read is probes.CheckScreen's decision, made
// by the caller on every use.
//
// It is deliberately NOT safe for concurrent use: the probe loop is a single
// goroutine (see runScreenProbes) and a mutex here would only hide a future
// caller that broke that assumption.
//
// Only the visible screen is cached. The Codex clear probe's second
// scrollback read is a different capture of a different size and stays
// uncached, so the map can stay keyed on the pane alone.
type ScreenTextCache struct {
	observer backend.Observer
	entries  map[backend.Ref]screenEntry
}

// TextReader is the one Kitty capability the Kitty-backed cache needs: read
// the text of a window matched by expression. The daemon's full Kitty client
// satisfies it, and narrowing to this seam is what keeps the cache free of the
// daemon root.
type TextReader interface {
	GetText(matchExpr, extent string) (string, error)
}

type screenEntry struct {
	screen backend.Screen
	err    error
}

// NewScreenTextCache builds the per-tick cache over the handler's Kitty client.
func NewScreenTextCache(client TextReader) *ScreenTextCache {
	return NewScreenObservationCache(kittyScreenObserver{client: client})
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
	screen, err := c.observer.Observe(pane)
	c.entries[pane] = screenEntry{screen: screen, err: err}
	return screen, err
}

// kittyScreenObserver reads a Kitty window's screen through the daemon's
// narrow text seam. It mirrors kitty.Backend.Observe, which needs the whole
// KittyClient: get-text reports neither geometry nor screen mode, and the
// instance is named by kind only.
type kittyScreenObserver struct {
	client TextReader
}

func (o kittyScreenObserver) Observe(pane backend.Ref) (backend.Screen, error) {
	windowID, err := strconv.Atoi(pane.ID)
	if err != nil || windowID <= 0 || pane.Scope != backend.ScopePane || strconv.Itoa(windowID) != pane.ID {
		return backend.Screen{}, &backend.InvalidRefError{Ref: pane, Reason: "not a Kitty window ID"}
	}
	text, err := o.client.GetText(kitty.MatchWindowByID(windowID), "screen")
	if err != nil {
		return backend.Screen{}, err
	}
	return backend.Screen{
		Instance:   runtimesession.BackendInstance{Kind: runtimesession.BackendKitty},
		Pane:       pane,
		Text:       text,
		CapturedAt: time.Now(),
	}, nil
}
