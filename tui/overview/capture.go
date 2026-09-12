// The live capture: which tiles are worth reading, how many of those reads may be
// out at once, who goes next, and what a reply is allowed to do.
//
// A grid is a different problem from a pane. The inspector reads ONE window and
// can afford to be simple about it — one read out, one catch-up behind it. An
// overview has up to nine visible windows, each of which wants a fresh picture
// several times a second, and reading all of them on every tick would put nine
// concurrent `kitten @ get-text` subprocesses against one Kitty for as long as the
// surface is open. So the capture is SCHEDULED rather than issued, and the
// schedule has three properties worth naming separately.
//
// BOUNDED. At most four reads exist at any moment, globally, and at most one per
// window. The global bound is what keeps the overview's cost independent of how
// many sessions the operator is running; the per-window bound is what stops a
// slow window from being asked again before it has answered, which would spend
// the whole budget on the one pane least able to use it.
//
// FAIR. Waiting windows are served from a FIFO queue, so a burst of ticks behind
// four slow reads does not starve the fifth tile: it is at the head of the queue
// and takes the first slot that comes free. Re-asking for a window already queued
// coalesces into the entry that is already there, which is what makes a run of
// ticks cost one read per window rather than one read per tick.
//
// RECOVERABLE. Every reply releases the slot its request took, whatever else
// happens to it. The two identities are separate for exactly this reason: the
// REQUEST ID decides the release, the GENERATION decides whether the text may be
// painted. A read of a tile the operator has since paged away from, resized, or
// closed hands its slot back and has its content dropped; settling on the
// generation instead would leave four slots held by replies nobody will ever
// accept and the surface would stop updating altogether.
//
// The controller is held by POINTER, alone among the model's state, and that is
// the design rather than an oversight. Every handler takes the model by value, so
// the rendered preview map is copied on write and an earlier generation of the
// model never sees a later one's captures. The SLOTS are the opposite kind of
// fact: a slot taken by a read dispatched from one copy has to be released by a
// reply delivered to another, and a value here would make "four in flight" a
// property of whichever copy was updated last. Naming the sharing in the type is
// better than leaving it to be discovered in a map header.
package overview

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
)

const (
	// maxCapturesInFlight is the global ceiling on concurrent reads.
	//
	// Four is a budget rather than a measurement: it is enough that a 2x2 grid
	// refreshes in one round and a 3x3 in three, and few enough that the surface
	// never has more subprocesses outstanding than a busy dashboard would. The
	// number is global — it spans pages, generations and anchor changes — because
	// what it bounds is the load on Kitty, which does not care which tile an
	// outstanding read was issued for.
	maxCapturesInFlight = 4

	// defaultCaptureInterval and minCaptureInterval are what an unset or
	// unreasonable preview cadence means here. Config normalization already
	// supplies the 500ms default and refuses anything under 50ms; this is the
	// same policy restated for a model built from a config that never went
	// through it, which is every model in a test.
	defaultCaptureInterval = 500 * time.Millisecond
	minCaptureInterval     = 50 * time.Millisecond
)

// captureIntervalFor is the configured capture cadence.
//
// It is deliberately NOT the topology interval. The window tree changes when a
// session is launched or closed and is read in seconds; a terminal's last nine
// lines change continuously and are read in milliseconds. Tying them together
// would either poll Kitty's window list ten times a second or leave every preview
// two seconds behind the pane beside it.
func captureIntervalFor(cfg *config.Config) time.Duration {
	d := time.Duration(cfg.Defaults.Preview.RefreshMs) * time.Millisecond
	if d < minCaptureInterval {
		return defaultCaptureInterval
	}
	return d
}

// captureTile is one visible tile's capture target: the window to read and the
// exact output region it will be drawn into.
type captureTile struct {
	windowID      int
	width, height int
}

// captureTarget is what the controller currently believes about a visible
// window: the generation any read of it must carry to be painted, and the size
// that read is clipped to.
type captureTarget struct {
	generation    int
	width, height int
}

// captureController is the scheduling half of the live capture. It decides
// nothing about what a tile SHOWS — that is the model's preview map — and it
// performs no I/O: it hands back requests, and the model turns those into
// commands through its seam.
type captureController struct {
	// targets holds exactly the windows that are visible right now. Membership is
	// the whole of "is this worth reading": a window with no target is off-page,
	// closed, or in a tile too small to draw output, and none of those may start a
	// read or keep a buffer.
	targets map[int]captureTarget

	// queue is the fair order waiting for a slot and queued is its membership, so
	// a repeated request collapses into the entry already there instead of adding
	// a second turn for the same window.
	queue  []int
	queued map[int]struct{}

	// byRequest maps a live request to the window it was issued for, and byWindow
	// is its inverse. byRequest alone is what bounds the world — its size is the
	// global in-flight count — and it is keyed by REQUEST precisely so a read for
	// a window that has since vanished can still find and release its slot.
	byRequest map[int]int
	byWindow  map[int]int

	nextRequestID  int
	nextGeneration int

	// stopped is dismissal. Once set, nothing is scheduled again and no reply is
	// accepted; replies still release their slots, because a released slot on a
	// surface that is closing costs nothing and an unreleased one is an invariant
	// quietly broken on the way out.
	stopped bool
}

func newCaptureController() *captureController {
	return &captureController{
		targets:   map[int]captureTarget{},
		queued:    map[int]struct{}{},
		byRequest: map[int]int{},
		byWindow:  map[int]int{},
	}
}

// retarget brings the target set into agreement with what is on screen.
//
// Three verdicts, and the difference between them is what a stored capture is
// still good for:
//
//   - A window that was not a target is NEW — newly visible, or the fresh anchor
//     of a restarted harness. It gets a generation and a place in the queue, so a
//     tile that has just appeared is read promptly rather than at the next tick.
//   - A window whose tile GREW cannot be served from what is already stored: the
//     cells it gained were never captured. It gets a new generation, which both
//     schedules the refetch and invalidates whatever read was already out for the
//     smaller tile.
//   - Anything else keeps its generation. A tile that shrank or did not move is
//     already covered by the capture it has, and bumping it would refetch every
//     window on every resize event a drag produces.
//
// Windows that are no longer visible simply do not appear in the new map. Their
// queued turns go with them and their in-flight reads are left to release their
// own slots on arrival.
func (c *captureController) retarget(visible []captureTile) {
	next := make(map[int]captureTarget, len(visible))
	var fresh []int
	for _, tile := range visible {
		existing, ok := c.targets[tile.windowID]
		grew := tile.width > existing.width || tile.height > existing.height
		if !ok || grew {
			c.nextGeneration++
			next[tile.windowID] = captureTarget{
				generation: c.nextGeneration,
				width:      tile.width,
				height:     tile.height,
			}
			fresh = append(fresh, tile.windowID)
			continue
		}
		existing.width, existing.height = tile.width, tile.height
		next[tile.windowID] = existing
	}
	c.targets = next
	c.dropQueuedWithoutTarget()
	for _, windowID := range fresh {
		c.enqueue(windowID)
	}
}

// enqueue asks for a read of one window, and is the only way a read is ever
// asked for.
//
// It declines in three cases and each is a bound rather than an optimization: a
// window with no target is not visible, a window already queued has a turn
// coming, and a window already in flight is at its per-window limit of one. The
// last is why a tick during a slow read costs nothing: the request is dropped
// here, and the reply that releases the slot is followed by the next tick asking
// again.
func (c *captureController) enqueue(windowID int) {
	if c.stopped {
		return
	}
	if _, ok := c.targets[windowID]; !ok {
		return
	}
	if _, ok := c.queued[windowID]; ok {
		return
	}
	if _, ok := c.byWindow[windowID]; ok {
		return
	}
	c.queued[windowID] = struct{}{}
	c.queue = append(c.queue, windowID)
}

// dropQueuedWithoutTarget removes the turns of windows that are no longer
// visible, preserving the order of the ones that remain.
//
// It rebuilds rather than filtering in place so the queue's order — which is the
// fairness property — is never rewritten underneath a slice some earlier value
// still refers to.
func (c *captureController) dropQueuedWithoutTarget() {
	if len(c.queue) == 0 {
		return
	}
	kept := make([]int, 0, len(c.queue))
	queued := make(map[int]struct{}, len(c.queue))
	for _, windowID := range c.queue {
		if _, ok := c.targets[windowID]; !ok {
			continue
		}
		kept = append(kept, windowID)
		queued[windowID] = struct{}{}
	}
	c.queue, c.queued = kept, queued
}

// dispatch takes as many waiting windows as there are free slots and turns them
// into stamped requests.
//
// Serving strictly from the head is the fairness rule: a window that has been
// waiting through three rounds of completions is ahead of one enqueued by the
// tick that just fired. A head entry that lost its target or acquired an
// in-flight read since it was queued is discarded rather than skipped over, so
// the loop always makes progress and a stale turn cannot hold the queue.
func (c *captureController) dispatch() []commands.CaptureRequest {
	if c.stopped {
		return nil
	}
	var out []commands.CaptureRequest
	for len(c.queue) > 0 && len(c.byRequest) < maxCapturesInFlight {
		windowID := c.queue[0]
		c.queue = c.queue[1:]
		delete(c.queued, windowID)

		target, ok := c.targets[windowID]
		if !ok {
			continue
		}
		if _, busy := c.byWindow[windowID]; busy {
			continue
		}

		c.nextRequestID++
		c.byRequest[c.nextRequestID] = windowID
		c.byWindow[windowID] = c.nextRequestID
		out = append(out, commands.CaptureRequest{
			WindowID:   windowID,
			Generation: target.generation,
			RequestID:  c.nextRequestID,
			Width:      target.width,
			Height:     target.height,
		})
	}
	return out
}

// release hands back the slot one reply's request took, and reports whether this
// reply is the one that owned it.
//
// False is a duplicate — a reply for a request that has already been settled —
// and it must change nothing at all. A second release would decrement the global
// count for a slot that is already free and eventually let a fifth read out.
//
// The window's own slot is only cleared when it still belongs to this request. A
// window whose read was superseded may already have a newer request against it,
// and the older reply must not free the newer one's place.
func (c *captureController) release(requestID int) bool {
	windowID, ok := c.byRequest[requestID]
	if !ok {
		return false
	}
	delete(c.byRequest, requestID)
	if current, held := c.byWindow[windowID]; held && current == requestID {
		delete(c.byWindow, windowID)
	}
	return true
}

// accepts reports whether a reply's CONTENT may be painted: the window is still
// visible and still pointed at what this read was issued against.
func (c *captureController) accepts(req commands.CaptureRequest) bool {
	if c.stopped {
		return false
	}
	target, ok := c.targets[req.WindowID]
	return ok && target.generation == req.Generation
}

// inFlight is the global count of outstanding reads.
func (c *captureController) inFlight() int { return len(c.byRequest) }

// stop is dismissal: schedule nothing further and accept nothing further.
//
// The queue is cleared rather than left to drain, because every entry in it is a
// read that would start after the operator asked to leave. The in-flight
// bookkeeping is deliberately kept so late replies still find their slots and
// release them through the ordinary path instead of a second one.
func (c *captureController) stop() {
	c.stopped = true
	c.queue, c.queued = nil, map[int]struct{}{}
}

// visibleTiles is the page on screen, in reading order, with each tile's output
// region already resolved.
//
// It is derived from the same Geometry and the same clamped selection the
// renderer draws from, so "visible" means exactly "drawn" — a tile the scheduler
// believed in but the renderer did not paint would be a read of a window nobody
// can see. Tiles with no room for output and sessions with no live window are
// left out: there is nothing to show for either, and a read would be spent on a
// picture that is never drawn.
func (m model) visibleTiles() []captureTile {
	if m.compact {
		return nil
	}
	g := MeasureMode(m.width, m.height, m.compact)
	capacity := g.Capacity()
	if capacity == 0 || len(m.sessions) == 0 {
		return nil
	}
	selected := clamp(m.selected, 0, len(m.sessions)-1)
	start := g.PageStart(g.PageOf(selected))

	out := make([]captureTile, 0, capacity)
	for pos := 0; pos < capacity; pos++ {
		index := start + pos
		if index >= len(m.sessions) {
			break
		}
		tile, ok := g.Tile(pos)
		if !ok {
			continue
		}
		width, height := outputExtent(tile)
		if width <= 0 || height <= 0 {
			continue
		}
		windowID := m.sessions[index].WindowID
		if windowID <= 0 {
			continue
		}
		out = append(out, captureTile{windowID: windowID, width: width, height: height})
	}
	return out
}

// reconcileCaptures is the one entry point for "what is on screen changed".
//
// Every event that can move a tile goes through it — a resize, a page change, a
// new window tree — and it does the two halves of the answer together: the
// scheduler is retargeted, and then the stored captures are brought into line
// with the targets that survived. Doing them apart would leave a window between
// the two in which a buffer belongs to no tile.
//
// The preview map is REBUILT rather than edited. Every handler takes the model by
// value and the map header is shared with the copy it was called on, so an
// in-place delete would reach backwards into a model the update loop has already
// moved past — which is harmless while Bubble Tea discards the old value, and
// exactly what makes two generations of the model indistinguishable in a test.
func (m *model) reconcileCaptures() {
	m.captures.retarget(m.visibleTiles())
	if len(m.previews) == 0 {
		return
	}
	kept := make(map[int]Preview, len(m.previews))
	for windowID, preview := range m.previews {
		target, ok := m.captures.targets[windowID]
		if !ok {
			// Off-page, closed, or re-anchored. The buffer is evicted with the
			// tile, which is both the memory bound and the correctness rule: a
			// retained capture of a window that is no longer on screen would be
			// painted, on its return, as the live state of a terminal that has
			// been running unobserved in the meantime.
			continue
		}
		kept[windowID] = clipPreview(preview, target.width, target.height)
	}
	m.previews = kept
}

// pumpCaptures turns whatever the scheduler will release into commands.
func (m *model) pumpCaptures() tea.Cmd {
	requests := m.captures.dispatch()
	if len(requests) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(requests))
	for _, req := range requests {
		cmds = append(cmds, m.deps.capture(req))
	}
	return tea.Batch(cmds...)
}

// handleCaptureTick re-arms the capture cadence and asks for every visible tile.
//
// Asking for all of them is safe because enqueue is idempotent per window: the
// ones already queued or already in flight are declined here, so a tick costs a
// request only for the tiles that have neither. That is what "repeated ticks
// coalesce rather than accumulate" means in practice — the queue is a set, and a
// hundred ticks behind a slow Kitty leave it exactly as long as one.
//
// A dismissed surface re-arms nothing. The ticker is the only thing keeping the
// capture alive, so declining to schedule the next one is how the scheduling
// stops.
func (m model) handleCaptureTick(commands.CaptureTickMsg) (tea.Model, tea.Cmd) {
	if m.captures.stopped {
		return m, nil
	}
	for _, tile := range m.visibleTiles() {
		m.captures.enqueue(tile.windowID)
	}
	cmds := []tea.Cmd{m.deps.captureTick(m.captureInterval)}
	if cmd := m.pumpCaptures(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleCaptureDone applies one completed read.
//
// The order is the contract. The slot is released FIRST and unconditionally,
// because every path below this line may decide to throw the content away and a
// slot that is only released on the happy path is a slot that leaks on exactly
// the failures this surface has to survive. Only then is the content judged.
//
// A reply that is not accepted still leaves the window wanting a picture, so it
// is re-queued rather than left for the next tick: the usual way to arrive here
// is that the tile was resized or re-anchored while its read was out, and the
// operator is looking at a tile whose content is now known to be wrong.
//
// A failure is content, not an outage. It is stored in the tile that asked for it
// and nowhere else, so eight healthy previews keep updating beside the one window
// that will not answer, and the next tick retries it on the ordinary cadence
// rather than through a backoff of its own.
func (m model) handleCaptureDone(msg commands.CaptureOutputMsg) (tea.Model, tea.Cmd) {
	if !m.captures.release(msg.Request.RequestID) {
		return m, nil
	}
	if !m.captures.accepts(msg.Request) {
		m.captures.enqueue(msg.Request.WindowID)
		return m, m.pumpCaptures()
	}
	preview := readyPreview(msg.Lines)
	if msg.Err != nil {
		preview = failedPreview(msg.Err.Error())
	}
	// Clipped again, against the target rather than against the request. A tile
	// that SHRANK keeps its generation — the cells it lost were already captured —
	// so an accepted reply can legitimately be wider or taller than the tile it is
	// about to be stored for, and the bound on retained text is the tile on screen
	// now rather than the one the read was issued against.
	target := m.captures.targets[msg.Request.WindowID]
	m.previews = withPreview(m.previews, msg.Request.WindowID, clipPreview(preview, target.width, target.height))
	return m, m.pumpCaptures()
}

// withPreview returns the preview map with one entry replaced, copying rather
// than writing through the shared header for the reason reconcileCaptures
// rebuilds it.
func withPreview(previews map[int]Preview, windowID int, preview Preview) map[int]Preview {
	next := make(map[int]Preview, len(previews)+1)
	for id, p := range previews {
		next[id] = p
	}
	next[windowID] = preview
	return next
}
