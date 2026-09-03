package daemon

// The blocked reason. Codex's classifier has always named which of its five
// prompts is on screen; until now the daemon threw the name away and kept only
// the icon it resolves to, so 🔑 could mean a command approval or a permission
// grant and nothing downstream could tell. The tests below pin the two halves of
// the plumbing that fixes that, and they are deliberately asymmetric because the
// writers are: the probe supplies the kind from a screen it actually read, and
// stampPromptKindLocked — running on EVERY publish path — is the only thing that
// clears it. A reason that outlived its prompt would be worse than no reason, so
// the clear is tested from the paths the probe never runs on, not just from the
// probe's own retraction.

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// promptKind reads the window's blocked reason.
func promptKind(t *testing.T, h *Handler, windowID int) string {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.PromptKind
}

// seedPromptKind writes the reason directly, which is how a case proves the
// stamp clears a kind it did not itself install — the real hazard, since the
// paths that clear (a hook stop, a watchdog expiry, a restore) are exactly the
// ones the probe never runs on.
func seedPromptKind(h *Handler, windowID int, kind string) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.ensureWindowStateLocked(windowID).PromptKind = kind
}

// probeCodexScreen runs one real ProbeCodexScreenPrompts pass over a Codex
// window showing `screen`, starting from `fromIcon`. It is the whole path —
// classifier, guard, reducer fold, publish — rather than a direct write, because
// what is under test is that the kind SURVIVES that path rather than that a
// field can be assigned.
func probeCodexScreen(h *Handler, windowID int, fromIcon, screen string) {
	fake := &fakeKittyClient{
		windows: codexProbeWindows(windowID, palette.BuildTitle("W:api", fromIcon, false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(windowID): screen,
		},
	}
	h.SetKittyClient(fake)
	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))
}

// TestCodexPromptKindReachesWindowState is the table the session asks for: one
// row per Codex prompt kind, from the screen a user would be looking at to the
// reason the window state ends up carrying.
//
// It doubles as the correctness proof for the two synthesized screens in
// codex_probe_helpers_test.go. ClassifyCodexPrompt tests its five arms in a
// fixed order with deliberately overlapping clause lists, so a screen written
// for the unanswered-confirmation kind that accidentally carried request-pane
// wording would classify as request_user_input instead — and land on the wrong
// row here rather than pass.
//
// The icon column is asserted alongside the kind because the two are a pair: the
// kind exists precisely to say what a 🔑 or ❓ stands for, and a row whose kind
// is right while its icon is wrong would mean the window is showing a reason for
// a prompt it is not displaying.
func TestCodexPromptKindReachesWindowState(t *testing.T) {
	cases := []struct {
		name     string
		screen   string
		wantKind activity.CodexPromptKind
		wantIcon string
	}{
		{
			name:     "edit approval",
			screen:   codexEditApprovalScreen,
			wantKind: activity.CodexPromptEditApproval,
			wantIcon: "question",
		},
		{
			name:     "request user input",
			screen:   codexRequestUserInputScreen,
			wantKind: activity.CodexPromptRequestUserInput,
			wantIcon: "question",
		},
		{
			name:     "unanswered confirmation",
			screen:   codexUnansweredConfirmationScreen,
			wantKind: activity.CodexPromptUnansweredConfirmation,
			wantIcon: "question",
		},
		{
			name:     "command approval",
			screen:   codexCommandApprovalScreen,
			wantKind: activity.CodexPromptCommandApproval,
			wantIcon: "key",
		},
		{
			name:     "permission grant",
			screen:   codexPermissionGrantScreen,
			wantKind: activity.CodexPromptPermissionGrant,
			wantIcon: "key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			probeCodexScreen(h, 42, "bolt", tc.screen)

			if got := promptKind(t, h, 42); got != string(tc.wantKind) {
				t.Fatalf("PromptKind = %q; want %q", got, tc.wantKind)
			}
			h.stateMu.Lock()
			icon := h.store[42].Effective.Icon
			h.stateMu.Unlock()
			if icon != tc.wantIcon {
				t.Fatalf("effective icon = %q; want %q (kind %s)", icon, tc.wantIcon, tc.wantKind)
			}
		})
	}
}

// TestPromptKindClearedLeavingBlockedClass is the clear, driven through
// publishEffectiveLocked alone. Every icon here is one the probe cannot produce,
// which is the point: the reason has to survive nothing and disappear on a hook
// stop, a watchdog expiry, or a restore just as reliably as on a probe
// retraction.
func TestPromptKindClearedLeavingBlockedClass(t *testing.T) {
	cases := []struct {
		name string
		icon string
		want string
	}{
		{name: "stop clears", icon: "stop"},
		{name: "bolt clears", icon: "bolt"},
		{name: "subagent clears", icon: "subagent"},
		{name: "compact clears", icon: "compact"},
		{name: "warning clears", icon: "warning"},
		{name: "error clears", icon: "error"},
		{name: "idle clears", icon: ""},
		// The two blocking icons are the whole exception, and they must not
		// clear: a window still showing a prompt still has a reason, and the
		// republishes that keep the icon there — watchdog sweeps, renderer
		// resyncs, ASL reconciliation — must not erase it between probes.
		{name: "key keeps", icon: "key", want: string(activity.CodexPromptCommandApproval)},
		{name: "question keeps", icon: "question", want: string(activity.CodexPromptCommandApproval)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			seedPromptKind(h, 7, string(activity.CodexPromptCommandApproval))

			publishIcon(h, 7, tc.icon)

			if got := promptKind(t, h, 7); got != tc.want {
				t.Fatalf("PromptKind after publishing %q = %q; want %q", tc.icon, got, tc.want)
			}
		})
	}
}

// TestPromptKindClearedWhenPromptAnswered is the end-to-end shape of the
// failure the clear exists to prevent: the user answers the prompt, the next
// probe sees no prompt and retracts, and the row must stop claiming a reason at
// the same moment it stops claiming the icon.
func TestPromptKindClearedWhenPromptAnswered(t *testing.T) {
	h := newTestHandler()
	probeCodexScreen(h, 42, "bolt", codexCommandApprovalScreen)
	if got := promptKind(t, h, 42); got != string(activity.CodexPromptCommandApproval) {
		t.Fatalf("PromptKind after the prompt appeared = %q; want %q", got, activity.CodexPromptCommandApproval)
	}

	// The same window a moment later, prompt answered and the turn running again.
	probeCodexScreen(h, 42, "key", codexWorkingTurnScreen)

	if got := promptKind(t, h, 42); got != "" {
		t.Fatalf("PromptKind after the prompt was answered = %q; want empty", got)
	}
}

// TestPromptKindReplacedAtTheSameIcon is why the probe stamps the kind on every
// fold rather than only on the transition that raises the icon. Codex can
// replace a command approval with a permission grant without the icon ever
// leaving 🔑, so there is no transition to hang the second write on — and a
// kind written only on the raise would leave the first prompt's name describing
// the second prompt.
func TestPromptKindReplacedAtTheSameIcon(t *testing.T) {
	h := newTestHandler()
	probeCodexScreen(h, 42, "bolt", codexCommandApprovalScreen)
	if got := promptKind(t, h, 42); got != string(activity.CodexPromptCommandApproval) {
		t.Fatalf("PromptKind = %q; want %q", got, activity.CodexPromptCommandApproval)
	}

	probeCodexScreen(h, 42, "key", codexPermissionGrantScreen)

	if got := promptKind(t, h, 42); got != string(activity.CodexPromptPermissionGrant) {
		t.Fatalf("PromptKind after a second 🔑 prompt = %q; want %q", got, activity.CodexPromptPermissionGrant)
	}
	h.stateMu.Lock()
	icon := h.store[42].Effective.Icon
	h.stateMu.Unlock()
	if icon != "key" {
		t.Fatalf("effective icon = %q; want key — the case only means anything if the icon never moved", icon)
	}
}

// TestPromptKindNotPersisted pins the deliberate omission from
// windowStateUserVar documented at the field. It is a one-site-not-two decision
// and therefore invisible in the diff that made it; without this the field could
// be quietly added to the payload later and nothing would object.
func TestPromptKindNotPersisted(t *testing.T) {
	h := newTestHandler()
	probeCodexScreen(h, 42, "bolt", codexCommandApprovalScreen)

	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	payload, err := st.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar: %v", err)
	}
	if want := string(activity.CodexPromptCommandApproval); strings.Contains(payload, want) {
		t.Fatalf("amx_state payload carries the prompt kind %q: %s", want, payload)
	}

	restored := *st
	if err := restored.UnmarshalUserVar(payload); err != nil {
		t.Fatalf("UnmarshalUserVar: %v", err)
	}
	if restored.PromptKind != "" {
		t.Fatalf("restored PromptKind = %q; want empty — a restart re-derives it from the screen", restored.PromptKind)
	}
}
