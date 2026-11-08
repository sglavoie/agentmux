//go:build tuitest

package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// runTimersFixture covers the per-row run timer across the bands it formats
// differently: a sub-minute burst, a multi-minute stretch, an hours-long
// stretch, a row deep enough into its watchdog threshold to carry the ⏳
// warning, and an idle row with no anchor at all. Every timestamp is a fixed
// offset from the driver's frozen clock, so each cell renders exact text. The
// idle row's own watchdog entry is inside the warning band on purpose: it must
// still render a bare em dash, because the warning belongs to rows that are
// actually running.
func runTimersFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "burst", Directory: "~/projects/burst", Profile: "work"},
			{Title: "review", Directory: "~/projects/review", Profile: "work"},
			{Title: "indexer", Directory: "~/projects/indexer", Profile: "work"},
			{Title: "stale", Directory: "~/projects/stale", Profile: "work"},
			{Title: "idle", Directory: "~/projects/idle", Profile: "personal"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "⚡🦊burst", true, managedVars("work", "claude", "burst", "~/projects/burst")),
		uitestTab(2, 21, "🤖🦊review", false, managedVars("work", "claude", "review", "~/projects/review")),
		uitestTab(3, 31, "🌀🦊indexer", false, managedVars("work", "claude", "indexer", "~/projects/indexer")),
		uitestTab(4, 41, "⚡🦊stale", false, managedVars("work", "claude", "stale", "~/projects/stale")),
		uitestTab(5, 51, "🛑🤖idle", false, managedVars("personal", "codex", "idle", "~/projects/idle")),
	)}
	// Anchors chosen so each row lands in a different formatting band:
	// 0:07, 3:07, 12h07, 9:41 (warning), and the em dash.
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	lastEvent := busySince
	const thresholdSec = 600
	return uitestFixture{
		Name:        "run_timers",
		Description: "busy stretches spanning the sub-minute, multi-minute, hours-long, warning, and idle timer bands",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(7 * time.Second)},
			21: {IconState: "subagent", BusySinceAt: busySince(3*time.Minute + 7*time.Second)},
			31: {IconState: "compact", BusySinceAt: busySince(12*time.Hour + 7*time.Minute)},
			41: {IconState: "bolt", BusySinceAt: busySince(9*time.Minute + 41*time.Second)},
			51: {IconState: "stop"},
		},
		Watchdog: map[int]protocol.WatchdogState{
			11: {LastAction: "active", LastEventAt: lastEvent(7 * time.Second), ThresholdSec: thresholdSec},
			21: {LastAction: "subagent", LastEventAt: lastEvent(3*time.Minute + 7*time.Second), ThresholdSec: thresholdSec},
			31: {LastAction: "compact", LastEventAt: lastEvent(45 * time.Second), ThresholdSec: thresholdSec},
			41: {LastAction: "active", LastEventAt: lastEvent(9*time.Minute + 41*time.Second), ThresholdSec: thresholdSec},
			51: {LastAction: "active", LastEventAt: lastEvent(22 * time.Minute), ThresholdSec: thresholdSec},
		},
	}
}

// unseenStopsFixture is two finished sessions that a tab title cannot tell
// apart. Both carry 🛑; only the daemon knows the user has read one of them and
// not the other. It exists so scenarios can pin the demotion that makes Needs
// Attention drain: the unread stop leads the list, the read one falls to Idle,
// and a working row holds Active in between so all three group labels render.
// The state names alone are ambiguous here on purpose — a fixture where the
// grouping could be reproduced from the titles would prove nothing.
func unseenStopsFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "unread", Directory: "~/projects/unread", Profile: "work"},
			{Title: "read", Directory: "~/projects/read", Profile: "work"},
			{Title: "working", Directory: "~/projects/working", Profile: "personal"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "🛑🦊unread", false, managedVars("work", "claude", "unread", "~/projects/unread")),
		uitestTab(2, 21, "🛑🦊read", false, managedVars("work", "claude", "read", "~/projects/read")),
		uitestTab(3, 31, "⚡🤖working", true, managedVars("personal", "codex", "working", "~/projects/working")),
	)}
	return uitestFixture{
		Name:        "unseen_stops",
		Description: "two identical 🛑 rows the daemon separates by seenness, plus one working row",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "stop", Unseen: true},
			21: {IconState: "stop"},
			31: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-42 * time.Second).Unix()},
		},
	}
}

// pinnedSessionsFixture is two pinned sessions — one waiting on a permission
// prompt, one working — beside an unpinned waiting, working, and rested row.
// The pin is the ★ in front of the tab title, which `amx pin-session` and the
// workspace sidebar write. It exists so scenarios can pin the band order: the
// pins lead under Pinned, the waiting pin stays there rather than joining
// Needs you, and Needs you follows them above Running and Idle.
func pinnedSessionsFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "pinwait", Directory: "~/projects/pinwait", Profile: "work"},
			{Title: "pinwork", Directory: "~/projects/pinwork", Profile: "personal"},
			{Title: "asks", Directory: "~/projects/asks", Profile: "work"},
			{Title: "busy", Directory: "~/projects/busy", Profile: "personal"},
			{Title: "rested", Directory: "~/projects/rested", Profile: "work"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "★🔑🦊pinwait", false, managedVars("work", "claude", "pinwait", "~/projects/pinwait")),
		uitestTab(2, 21, "★⚡🤖pinwork", false, managedVars("personal", "codex", "pinwork", "~/projects/pinwork")),
		uitestTab(3, 31, "🔑🦊asks", false, managedVars("work", "claude", "asks", "~/projects/asks")),
		uitestTab(4, 41, "⚡🤖busy", true, managedVars("personal", "codex", "busy", "~/projects/busy")),
		uitestTab(5, 51, "🛑🦊rested", false, managedVars("work", "claude", "rested", "~/projects/rested")),
	)}
	return uitestFixture{
		Name:        "pinned_sessions",
		Description: "two pinned rows (one waiting, one working) above an unpinned waiting, working, and rested row",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "key"},
			21: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-90 * time.Second).Unix()},
			31: {IconState: "key"},
			41: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-42 * time.Second).Unix()},
			51: {IconState: "stop"},
		},
	}
}

// blockedReasonsFixture is four Needs you rows that a tab title cannot tell
// apart — two 🔑 and two ❓ — where only the daemon knows what each one is
// actually waiting for. It covers the three cases the Status column has to get
// right at once:
//
//   - a Codex permission prompt and a Claude permission prompt, whose kind
//     strings are deliberately different (`command_approval` vs `permission`,
//     because the two harnesses share one untyped field) and which must
//     nonetheless both read `Needs permission`: the status names what is being
//     asked of the user, not which dialog asked it;
//   - a Codex request-for-input prompt, which reads `Needs input` rather than
//     borrowing the permission sentence;
//   - a blocked row with NO reason at all, which is the ordinary steady state
//     for a hook-raised icon no screen probe has labelled yet. It falls back to
//     `Awaiting reply` rather than going blank, because a row in Needs you with
//     an empty status would be a row that cannot say why it is there.
//
// The `working` row holds Running so the shared-column rule is visible too: it
// is not in the attention group, has nothing to explain, and still lands its
// status and time in the same cells as the rows above it.
//
// The kinds are written as bare strings rather than screenstate constants
// because that is exactly what a fixture stands in for: one daemon state query,
// whose Reason field is an untyped wire string. The dashboard's own map from
// those strings to tags is pinned separately, in package tui/dashboard.
func blockedReasonsFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "approve", Directory: "~/projects/approve", Profile: "personal"},
			{Title: "grant", Directory: "~/projects/grant", Profile: "work"},
			{Title: "choose", Directory: "~/projects/choose", Profile: "personal"},
			{Title: "silent", Directory: "~/projects/silent", Profile: "work"},
			{Title: "working", Directory: "~/projects/working", Profile: "work"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "🔑🤖approve", false, managedVars("personal", "codex", "approve", "~/projects/approve")),
		uitestTab(2, 21, "🔑🦊grant", false, managedVars("work", "claude", "grant", "~/projects/grant")),
		uitestTab(3, 31, "❓🤖choose", false, managedVars("personal", "codex", "choose", "~/projects/choose")),
		uitestTab(4, 41, "❓🦊silent", false, managedVars("work", "claude", "silent", "~/projects/silent")),
		uitestTab(5, 51, "⚡🦊working", true, managedVars("work", "claude", "working", "~/projects/working")),
	)}
	return uitestFixture{
		Name:        "blocked_reasons",
		Description: "four identically-iconed blocked rows the daemon explains with different prompt kinds, one of them unlabelled",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "key", Reason: "command_approval"},
			21: {IconState: "key", Reason: "permission"},
			31: {IconState: "question", Reason: "request_user_input"},
			41: {IconState: "question"},
			51: {IconState: "bolt"},
		},
	}
}
