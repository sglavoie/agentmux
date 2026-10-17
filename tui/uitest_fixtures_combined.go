// The two fixture worlds the redesign's combined scenarios need and that none
// of the single-surface fixtures can stand in for.
//
// A combined scenario is one that crosses surfaces inside a single run — the
// inspector holding its position while the queue underneath it is refiltered,
// a read that fails and then succeeds — and both of those need a world that is
// simultaneously several things. `paused_output` has the scrollback but only two
// rows, so nothing can move underneath the pane; `triage_integration` has the
// rows but no scrollback, so the pane has nothing to hold. Neither can be
// widened into the other without changing what the scenarios already committed
// against them assert.
//
// They live in their own file for the reason uitest_fixtures_redesign.go does:
// the worlds an increment needs are easier to retire, or to recognize as still
// needed, when they are not interleaved with the ones that came before.

package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// pausedQueueFixture is a queue deep enough to reorder under a held inspector.
//
// The design constraint is that the pane's hold has to be observable ACROSS a
// change to the list, which needs three things at once and is why no existing
// fixture serves: a scrollback longer than the pane (so following and holding
// look different), rows in more than one urgency band (so removing a band moves
// the survivors), and a selected row that survives the change (so the hold is
// being tested rather than the retarget that a changed selection would trigger).
//
// The rows:
//
//   - `alpha` and `bravo` are working. They are the pair a filter keeps, and
//     the reason there are two of them is that a single survivor could not show
//     an ORDER surviving — only a presence.
//   - `charlie` is an unread finished turn, so it sorts above both of them into
//     Needs you. It is what a filter removes, and removing it is what moves the
//     working pair up the screen.
//   - `delta` is a seen finished turn, so it sorts below into Idle. It is the
//     second thing a filter removes, from the other side, so the movement is
//     not confusable with the list simply shrinking from the top.
//   - `ρυθμός-δοκιμή` is a working row whose name is outside ASCII and whose
//     directory is long enough to need truncating in the inspector's Dir line.
//     It is here rather than in a fixture of its own because the interesting
//     question about a wide name is how it behaves in a list with ordinary
//     names beside it — a fixture of nothing but wide names could not show a
//     column that failed to align.
//
// Every row shares one scrollback because the pane reads exactly one window at
// a time; which window was asked is what the recording client captures, and a
// per-window buffer would only let a scenario assert that fact twice.
func pausedQueueFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: uitestPreviewDefaults(),
			// The one fixture that asks to be confirmed. A round trip through a
			// confirmation prompt is part of what this world exists for, and the
			// prompt is opt-in configuration rather than a property of any
			// session — so without a fixture that turns it on, the surface is
			// unreachable from a committed scenario at all.
			ConfirmDestructive: true,
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "alpha", Directory: "~/projects/alpha", Profile: "work"},
			{Title: "bravo", Directory: "~/projects/bravo", Profile: "work"},
			{Title: "charlie", Directory: "~/projects/charlie", Profile: "personal"},
			{Title: "delta", Directory: "~/projects/delta", Profile: "personal"},
			{Title: "ρυθμός-δοκιμή", Directory: "~/projects/very-long-directory-name/nested/deeper/ρυθμός-δοκιμή", Profile: "work"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊alpha", true, managedVars("work", "claude", "alpha", "~/projects/alpha")),
		uitestTab(2, 21, "⚡🦊bravo", false, managedVars("work", "claude", "bravo", "~/projects/bravo")),
		uitestTab(3, 31, "🛑🤖charlie", false, managedVars("personal", "codex", "charlie", "~/projects/charlie")),
		uitestTab(4, 41, "🛑🤖delta", false, managedVars("personal", "codex", "delta", "~/projects/delta")),
		uitestTab(5, 51, "⚡🦊ρυθμός-δοκιμή", false, managedVars("work", "claude", "ρυθμός-δοκιμή",
			"~/projects/very-long-directory-name/nested/deeper/ρυθμός-δοκιμή")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	return uitestFixture{
		Name:        "paused_queue",
		Description: "a five-row queue spanning all three urgency bands over a scrollback long enough to hold, so the list can be reordered underneath a paused inspector",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		Windows:     windows,
		Text:        numberedScrollback(60),
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(42 * time.Second)},
			21: {IconState: "bolt", BusySinceAt: busySince(3*time.Minute + 7*time.Second)},
			31: {IconState: "stop", Unseen: true},
			41: {IconState: "stop"},
			51: {IconState: "bolt", BusySinceAt: busySince(12 * time.Second)},
		},
	}
}

// noSessionsFixture is a configured operator with nothing running.
//
// It is not the same world as "a filter matched nothing", and the dashboard
// says so in different words, which is the whole reason it exists: the filtered
// hero offers `esc`, the empty one offers `l`. A scenario driven from a
// populated fixture can only ever reach the first, so the second needs a world
// where the window list itself is empty.
//
// The config still names two projects. An operator with no config at all is a
// different (and rarer) state, and a fixture with neither windows nor projects
// could not tell "nothing is running" apart from "nothing is set up" — the
// projects are what make the empty hero's launch hint mean something.
func noSessionsFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: uitestPreviewDefaults(),
		},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "site", Directory: "~/projects/site", Profile: "work"},
		},
	}
	return uitestFixture{
		Name:        "no_sessions",
		Description: "a configured operator with no live sessions at all, which is the only world that reaches the empty hero rather than the filtered one",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		Windows:     []kitty.OSWindow{uitestOSWindow()},
	}
}

// previewFailureFixture is a world whose first read fails and whose every read
// after it succeeds.
//
// One failure and not more, because the property under test is recovery: a
// fixture that failed forever would prove the pane can report an error and
// nothing about whether it can stop. The second row exists so the recovery can
// be driven by moving the cursor as well as by reopening the pane — those are
// two different paths into the read controller, and a one-row fixture could
// only exercise the second.
//
// The scrollback is numbered like every other read fixture's, so the scenario
// that watches the pane recover asserts a POSITION in the buffer rather than a
// piece of prose, and cannot pass against a pane that painted something else.
func previewFailureFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: uitestPreviewDefaults(),
		},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: "unreadable", Directory: "~/projects/unreadable", Profile: "work"},
			{Title: "neighbour", Directory: "~/projects/neighbour", Profile: "work"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊unreadable", true, managedVars("work", "claude", "unreadable", "~/projects/unreadable")),
		uitestTab(2, 21, "⚡🦊neighbour", false, managedVars("work", "claude", "neighbour", "~/projects/neighbour")),
	)}
	return uitestFixture{
		Name:         "preview_failure",
		Description:  "a pane whose first read fails and whose reads after it succeed, so a scenario can watch the inspector report the error and then recover",
		Size:         UITestSize{Width: 120, Height: 32},
		Config:       cfg,
		Windows:      windows,
		Text:         numberedScrollback(40),
		TextFailures: 1,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-42 * time.Second).Unix()},
			21: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-9 * time.Second).Unix()},
		},
	}
}

func setupProfileFixture() uitestFixture {
	f := noSessionsFixture()
	f.Name = "setup_profile"
	f.Description = "first session setup with no agent profiles"
	f.Config.Profiles, f.Config.Accounts = nil, nil
	f.Config.Projects, f.Config.Sessions = nil, nil
	return f
}
func setupProjectFixture() uitestFixture {
	f := noSessionsFixture()
	f.Name = "setup_project"
	f.Description = "first session setup with a profile but no project"
	f.Config.Projects, f.Config.Sessions = nil, nil
	return f
}
