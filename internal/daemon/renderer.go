package daemon

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	renderInterval          = 2 * time.Second
	renderDebounce          = 50 * time.Millisecond
	renderWarnAfterFailures = 3
)

type socketScopedKittyClient interface {
	WithSocket(socket string) *kitty.SubprocessClient
}

type renderCandidate struct {
	windowID int
	seq      uint64
	title    string
	userVar  string
	dirty    bool
}

func (h *Handler) runRenderer(ctx context.Context) {
	ticker := time.NewTicker(renderInterval)
	defer ticker.Stop()

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	stopDebounce := func() {
		if debounceTimer == nil {
			return
		}
		if !debounceTimer.Stop() {
			select {
			case <-debounceTimer.C:
			default:
			}
		}
		debounceTimer = nil
		debounceC = nil
	}

	for {
		select {
		case <-ctx.Done():
			stopDebounce()
			return
		case <-h.renderCh:
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(renderDebounce)
				debounceC = debounceTimer.C
			}
		case <-debounceC:
			debounceTimer = nil
			debounceC = nil
			h.renderTick(time.Now())
		case now := <-ticker.C:
			stopDebounce()
			h.renderTick(now)
		}
	}
}

func (h *Handler) restoreStateFromKitty(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("renderer: startup list windows failed", "err", err)
		return
	}
	h.restoreFromUserVars(windows, now)
	h.renderSnapshot(windows)
}

func (h *Handler) renderTick(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("renderer: list windows failed", "err", err)
		return
	}
	h.runSnapshotTick(now, windows)
}

func (h *Handler) runSnapshotTick(now time.Time, windows []kitty.OSWindow) {
	h.shiftActivityAfterTickGap(now)
	h.refreshMutedFromSnapshot(windows)
	h.pruneClosedWindows(liveWindowIDs(windows))
	h.ProbeCodexScreenPromptsWithSnapshot(now, windows)
	h.ProbeClaudeContextLimitWithSnapshot(now, windows)
	h.SweepStaleWithSnapshot(now, windows)
	h.SweepHookSilenceWithSnapshot(now, windows)
	h.renderSnapshot(windows)
}

func (h *Handler) shiftActivityAfterTickGap(now time.Time) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.lastRenderTickAt.IsZero() {
		h.lastRenderTickAt = now
		return
	}
	gap := now.Sub(h.lastRenderTickAt)
	h.lastRenderTickAt = now
	if gap <= 2*renderInterval {
		return
	}
	for _, st := range h.store {
		if !st.Activity.IsZero() {
			st.Activity = st.Activity.Add(gap)
		}
	}
	for account, seenAt := range h.lastSeenEventAt {
		if !seenAt.IsZero() {
			h.lastSeenEventAt[account] = seenAt.Add(gap)
		}
	}
}

func (h *Handler) refreshMutedFromSnapshot(windows []kitty.OSWindow) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for i := range windows {
		for j := range windows[i].Tabs {
			tabMuted := palette.ParseTitleParts(windows[i].Tabs[j].Title).Muted
			for k := range windows[i].Tabs[j].Windows {
				win := windows[i].Tabs[j].Windows[k]
				if st, ok := h.windowStateLocked(win.ID); ok {
					st.Muted = tabMuted || win.UserVars.Mute != ""
				}
			}
		}
	}
}

// setMutedFromSnapshot records the cc_mute/title mute state read from a live
// Kitty snapshot. Hook requests may carry the legacy Muted wire field, but the
// daemon deliberately never trusts that client-provided value.
func (h *Handler) setMutedFromSnapshot(windowID int, muted bool) {
	if windowID <= 0 {
		return
	}
	h.stateMu.Lock()
	h.ensureWindowStateLocked(windowID).Muted = muted
	h.stateMu.Unlock()
}

func (h *Handler) renderSnapshot(windows []kitty.OSWindow) {
	candidates := h.renderCandidates(windows)
	for _, candidate := range candidates {
		client := h.kittyClientForWindow(candidate.windowID)
		if client == nil {
			continue
		}

		var renderErr error
		match := kitty.MatchTabByWindowID(candidate.windowID)
		if candidate.title != "" {
			renderErr = client.SetTabTitle(match, candidate.title)
		}
		if renderErr == nil && candidate.dirty {
			renderErr = client.SetUserVars(kitty.MatchWindowByID(candidate.windowID), map[string]string{
				"amx_state": candidate.userVar,
			})
		}
		h.finishRenderCandidate(candidate, renderErr)
	}
}

func (h *Handler) renderCandidates(windows []kitty.OSWindow) []renderCandidate {
	var out []renderCandidate
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	for i := range windows {
		for j := range windows[i].Tabs {
			tab := &windows[i].Tabs[j]
			owner, ok := h.renderTitleOwnerLocked(tab)
			if !ok {
				continue
			}
			st, ok := h.windowStateLocked(owner.ID)
			if !ok {
				continue
			}

			parts := palette.ParseTitleParts(tab.Title)
			parts.State = desiredIconForState(st)
			if st.LoopingSet || st.Looping {
				parts.Looping = st.Looping
			}
			nextTitle := palette.BuildTitleParts(parts)
			if nextTitle == tab.Title {
				nextTitle = ""
			}

			userVar := ""
			if st.Dirty {
				var err error
				userVar, err = st.MarshalUserVar()
				if err != nil {
					st.RenderFailures++
					st.LastRenderError = err.Error()
					h.syncRendererMetadataLocked(owner.ID)
					continue
				}
			}

			if nextTitle == "" && !st.Dirty {
				continue
			}
			out = append(out, renderCandidate{
				windowID: owner.ID,
				seq:      st.Seq,
				title:    nextTitle,
				userVar:  userVar,
				dirty:    st.Dirty,
			})
		}
	}
	return out
}

func (h *Handler) renderTitleOwnerLocked(tab *kitty.Tab) (*kitty.Window, bool) {
	if owner, ok := kitty.FindTabTitleOwner(tab); ok {
		return owner, true
	}
	if tab == nil {
		return nil, false
	}
	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if _, ok := h.windowStateLocked(win.ID); !ok {
			continue
		}
		if fallback == nil {
			fallback = win
		}
		if win.IsActive {
			return win, true
		}
	}
	if fallback == nil {
		return nil, false
	}
	return fallback, true
}

func desiredIconForState(st *WindowState) string {
	if st == nil {
		return ""
	}
	if st.DesiredIcon != "" || st.LastAction == "" {
		return st.DesiredIcon
	}
	return IconState(st.LastAction)
}

func (h *Handler) finishRenderCandidate(candidate renderCandidate, renderErr error) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.windowStateLocked(candidate.windowID)
	if !ok || st.Seq != candidate.seq {
		return
	}
	if renderErr != nil {
		st.Dirty = true
		st.RenderFailures++
		st.LastRenderError = renderErr.Error()
		h.syncRendererMetadataLocked(candidate.windowID)
		if st.RenderFailures >= renderWarnAfterFailures && st.RenderFailures%renderWarnAfterFailures == 0 {
			slog.Warn("renderer: kitty write failed",
				"window_id", candidate.windowID,
				"failures", st.RenderFailures,
				"err", renderErr,
			)
		}
		return
	}
	st.Dirty = false
	st.RenderFailures = 0
	st.LastRenderError = ""
	h.syncRendererMetadataLocked(candidate.windowID)
}

func (h *Handler) kittyClientForList() kittyTitleClient {
	h.stateMu.Lock()
	socket := h.lastKittyListenOn
	client := h.kittyClient
	h.stateMu.Unlock()
	return scopedKittyClient(client, socket)
}

func (h *Handler) kittyClientForWindow(windowID int) kittyTitleClient {
	h.stateMu.Lock()
	socket := h.kittySockets[windowID]
	if socket == "" {
		socket = h.lastKittyListenOn
	}
	client := h.kittyClient
	h.stateMu.Unlock()
	return scopedKittyClient(client, socket)
}

// hookKittyClient returns the Kitty client used for hook-time ListWindows and
// GetText calls, scoped to the stable socket carried by the request. It falls
// back to the daemon's established client only when the request has no reusable
// (transferable) address, so a stale indicator forwarding an "fd:" descriptor
// cannot force a dial through a process-local endpoint.
func (h *Handler) hookKittyClient(req Request) kittyTitleClient {
	if isReusableKittySocket(req.KittyListenOn) {
		return scopedKittyClient(h.kittyClient, strings.TrimSpace(req.KittyListenOn))
	}
	return h.kittyClient
}

func scopedKittyClient(client kittyTitleClient, socket string) kittyTitleClient {
	if socket == "" {
		return client
	}
	if scoped, ok := client.(socketScopedKittyClient); ok {
		return scoped.WithSocket(socket)
	}
	return client
}

func liveWindowIDs(windows []kitty.OSWindow) map[int]bool {
	live := make(map[int]bool)
	for _, ow := range windows {
		for _, tab := range ow.Tabs {
			for _, win := range tab.Windows {
				live[win.ID] = true
			}
		}
	}
	return live
}
