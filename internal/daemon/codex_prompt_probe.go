package daemon

import (
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	screenPromptProbeEvent  = "ScreenPromptProbe"
	screenClearProbeEvent   = "ScreenClearProbe"
	screenStopProbeEvent    = "ScreenStopProbe"
	screenPromptProbeSource = "codex_screen"
)

// ProbeCodexScreenPrompts scans managed Codex windows and applies any visible
// prompt found on the current screen. The screen is authoritative for Codex:
// edit/request_user_input prompts can replace existing bolt/key guards with the
// lower-priority elicitation state, while terminal and special-purpose icons are
// never overwritten. A stopped Codex tab gets one extra conservative check for
// the cleared idle UI because Codex does not emit an immediate hook for idle
// `/clear`.
func (h *Handler) ProbeCodexScreenPrompts(now time.Time) {
	if h.kittyClient == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := h.kittyClient.ListWindows()
	if err != nil {
		slog.Debug("codex prompt probe: list windows failed", "err", err)
		return
	}
	h.probeCodexScreenPromptsWithSnapshot(now, screenProbeSnapshot{windows: windows, reducerReceipts: reducerReceipts})
	h.renderSnapshot(windows)
}

func (h *Handler) ProbeCodexScreenPromptsWithSnapshot(now time.Time, windows []kitty.OSWindow) {
	h.probeCodexScreenPromptsWithSnapshot(now, h.screenProbeSnapshotForWindows(windows))
}

func (h *Handler) probeCodexScreenPromptsWithSnapshot(now time.Time, snapshot screenProbeSnapshot) {
	bindings := h.screenProbeBindings(snapshot, harness.Codex)
	bindings = append(bindings, h.screenProbeTerminalBindings(snapshot, harness.Codex, now.Unix())...)
	for _, binding := range bindings {
		_, tab, target, ok := kitty.FindWindowLocationByID(snapshot.windows, binding.TargetWindowID)
		if !ok {
			continue
		}
		h.probeCodexBinding(now, binding, palette.ParseTitleParts(tab.Title), target.UserVars)
	}

	// Preserve the historical foreground fallback for a tab with no managed
	// target. The executable check is positive Codex producer evidence; encode
	// that as an explicit known-producer direct binding rather than allowing an
	// empty harness to fall through configuration's default-to-Claude path.
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			win, ok := codexPromptProbeTarget(tab)
			if !ok {
				continue
			}
			binding, found := newScreenProbeBinding(
				snapshot,
				conversation.Identity{Harness: harness.Codex},
				win.ID,
				win.ID,
			)
			if !found {
				continue
			}
			binding.legacyDirectManaged = true
			h.probeCodexBinding(now, binding, palette.ParseTitleParts(tab.Title), win.UserVars)
		}
	}
}

func (h *Handler) probeCodexBinding(
	now time.Time,
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) {
	titleParts, userVars, ok := h.codexProbeTargetContext(binding, titleParts, userVars)
	if !ok {
		return
	}
	if titleParts.State == "stop" {
		h.probeCodexClearWindow(now, binding, titleParts, userVars)
		return
	}
	if binding.terminalProducer {
		// A terminal binding exists only to authorize the producer's routed
		// cleared-screen check. It never reopens prompt or idle classification.
		return
	}
	if !codexPromptProbeCanOverwrite(titleParts.State) {
		return
	}
	h.probeCodexWindow(now, binding, titleParts, userVars)
	// Prompt classification wins first. If it found no prompt and left the
	// receipt current, the idle classifier may settle a genuinely finished bolt.
	if titleParts.State == "bolt" {
		h.probeCodexIdleWindow(now, binding, titleParts, userVars)
	}
}

// codexProbeTargetContext derives presentation and eligibility from the managed
// target, not from whichever pane currently renders the shared tab title. This
// preserves an inactive borrowed target's title/profile/owner metadata while
// the binding continues to read the producer pane.
func (h *Handler) codexProbeTargetContext(
	binding screenProbeBinding,
	titleParts palette.TitleParts,
	userVars kitty.UserVars,
) (palette.TitleParts, kitty.UserVars, bool) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if !h.screenProbeBindingCurrentLocked(binding) {
		return titleParts, userVars, false
	}
	st, ok := h.windowStateLocked(binding.TargetWindowID)
	if !ok || st == nil {
		return titleParts, userVars, true
	}
	// A legacy direct stopped title may intentionally repair title-only drift
	// while the in-process reducer is empty (including a deferred Stop that has
	// advanced only the receipt). Preserve that snapshot stop candidate; routed
	// bindings always derive their branch from the target reducer.
	if st.Effective.Icon != "" || !binding.legacyDirectManaged || titleParts.State != "stop" {
		titleParts.State = st.Effective.Icon
	}
	titleParts.Muted = titleParts.Muted || st.Muted
	if st.LastSessionTitle != "" {
		titleParts.Base = st.LastSessionTitle
	}
	if st.LastAccount != "" {
		userVars.Profile = st.LastAccount
	}
	if targetHarness := harness.NormalizeKnown(st.LastHarness); targetHarness != "" {
		userVars.Harness = targetHarness
	}
	return titleParts, userVars, true
}

func codexPromptProbeTarget(tab *kitty.Tab) (*kitty.Window, bool) {
	if _, ok := kitty.FindTabTitleOwner(tab); ok {
		return nil, false
	}
	if tab == nil {
		return nil, false
	}

	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if !codexPromptProbeShouldScan(*win) {
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

func codexPromptProbeShouldScan(win kitty.Window) bool {
	switch win.UserVars.EffectiveHarness() {
	case harness.Codex:
		return true
	case "":
		return codexPromptProbeHasCodexForeground(win)
	default:
		return false
	}
}

func codexPromptProbeHasCodexForeground(win kitty.Window) bool {
	if codexPromptProbeCmdlineIsCodex(win.Cmdline) {
		return true
	}
	for _, proc := range win.ForegroundProcesses {
		if codexPromptProbeCmdlineIsCodex(proc.Cmdline) {
			return true
		}
	}
	return false
}

// codexPromptProbeCmdlineIsCodex accepts only an interactive Codex invocation.
// A non-interactive run such as `codex exec` is a background subprocess in an
// unmanaged pane, not a session the dashboard reports on.
func codexPromptProbeCmdlineIsCodex(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}
	if !strings.EqualFold(filepath.Base(cmdline[0]), "codex") {
		return false
	}
	return !harness.InvocationIsPipeMode(harness.Codex, cmdline[1:])
}

func codexPromptProbeCanOverwrite(iconState string) bool {
	switch iconState {
	case "", "bolt", "key", "question":
		return true
	default:
		return false
	}
}
