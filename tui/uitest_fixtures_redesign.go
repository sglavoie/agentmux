// This file holds the fixture worlds the dashboard redesign needs and that no
// existing fixture can stand in for: colliding row names, a scrollback long
// enough to be followed or held, a health answer other than "no daemon", and
// rows carrying more than one reason to want the operator at once.
//
// They are separated from uitest_fixtures.go because they are established
// ahead of the surfaces that will read them. Each one is asserted against
// TODAY's rendering, so it is a fixture and not a promise: when the surface
// that needs it lands, the world it is driven against is already committed,
// already deterministic, and already known to load.

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/commands"
)

// duplicateNamesFixture is six rows whose names collide, arranged so that each
// rung of the disambiguation ladder is the ONLY rung that separates one pair.
//
//   - `api` twice, same profile, one repository, two checkouts. Only the branch
//     (or the worktree the branch is checked out in) tells them apart, so this
//     pair is what the first rung exists for.
//   - `web` twice, different profiles, different repositories, and — this is the
//     trap — the SAME branch name, `main`. Branch is useless here precisely
//     because `main` is the most common branch there is, so the pair falls
//     through to the profile.
//   - `docs` twice, same profile, same repository, same branch, same directory.
//     Nothing about the work distinguishes them; they are two harness sessions
//     open on the same checkout, which is an ordinary thing to do and the reason
//     the ladder needs a last rung that always terminates. Only a stable session
//     identifier separates them.
//
// `notes` is unique and is not decoration: a collision is a property of the
// live set, so the fixture has to contain a row that must NOT be renamed. It is
// also why the two `web` rows sit under different profiles — a rename driven by
// the configured project list rather than by what is on screen would decorate
// every row that shares a title with something in the config, including rows
// that are alone on the dashboard.
//
// Every row is `bolt` on purpose. Urgency is not the subject here, and states
// that spread the rows across three groups would let a reader tell a pair apart
// by the band it sits in rather than by its name.
func duplicateNamesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: uitestPreviewDefaults(),
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "api", Directory: "~/projects/worktrees/api-feat-x", Profile: "work"},
			{Title: "web", Directory: "~/projects/web", Profile: "work"},
			{Title: "web", Directory: "~/sites/web", Profile: "personal"},
			{Title: "docs", Directory: "~/projects/docs", Profile: "personal"},
			{Title: "notes", Directory: "~/notes", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🦊api", false, managedVars("work", "claude", "api", "~/projects/worktrees/api-feat-x")),
		uitestTab(3, 31, "⚡🦊web", false, managedVars("work", "claude", "web", "~/projects/web")),
		uitestTab(4, 41, "⚡🤖web", false, managedVars("personal", "codex", "web", "~/sites/web")),
		uitestTab(5, 51, "⚡🤖docs", false, managedVars("personal", "codex", "docs", "~/projects/docs")),
		uitestTab(6, 61, "⚡🤖docs", false, managedVars("personal", "codex", "docs", "~/projects/docs")),
		uitestTab(7, 71, "⚡🤖notes", false, managedVars("personal", "codex", "notes", "~/notes")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	return uitestFixture{
		Name:        "duplicate_names",
		Description: "three colliding row names separated only by branch, only by profile, and only by session identity, beside one name that must stay alone",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(42 * time.Second), Branch: "main", Dirty: true},
			21: {IconState: "bolt", BusySinceAt: busySince(3*time.Minute + 7*time.Second), Branch: "amx/feat-x"},
			31: {IconState: "bolt", BusySinceAt: busySince(12 * time.Second), Branch: "main"},
			41: {IconState: "bolt", BusySinceAt: busySince(1*time.Minute + 5*time.Second), Branch: "main"},
			// Identical anchors, deliberately. A different elapsed time would be
			// one more thing separating the pair on screen, and this pair exists
			// to have nothing separating it but identity.
			51: {IconState: "bolt", BusySinceAt: busySince(8 * time.Second)},
			61: {IconState: "bolt", BusySinceAt: busySince(8 * time.Second)},
			71: {IconState: "bolt", BusySinceAt: busySince(30 * time.Second)},
		},
		Families: map[int]uitestFamily{
			11: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			21: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			31: {Key: "git:web-work", Label: "web", IsRepository: true, PrimaryRoot: "~/projects/web"},
			41: {Key: "git:web-personal", Label: "web", IsRepository: true, PrimaryRoot: "~/sites/web"},
			51: {Key: "git:docs", Label: "docs", IsRepository: true, PrimaryRoot: "~/projects/docs"},
			61: {Key: "git:docs", Label: "docs", IsRepository: true, PrimaryRoot: "~/projects/docs"},
			71: {Key: "project:notes", Label: "notes", PrimaryRoot: "~/notes"},
		},
	}
}

// pausedOutputFixture is one selected session with a scrollback long enough to
// be followed — and, once the inspector can hold its position, long enough for
// holding it to show something different from following it.
//
// The lines are numbered from 01 so that WHICH window onto the buffer is on
// screen is readable from the screen itself. That is the whole design: a pane
// showing the tail and a pane showing a held snapshot are both "some lines of
// text", and a fixture of undifferentiated prose could not tell them apart. A
// scenario asserting `output line 40` is asserting that the pane follows; the
// same fixture asserting `output line 12` later will be asserting that it does
// not.
//
// The buffer is deliberately longer than any pane the fixture's size can
// produce, so the tail is a strict suffix rather than the whole thing. A
// scrollback that fit entirely on screen would make "following" and "holding"
// the same picture and the fixture vacuous.
func pausedOutputFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: uitestPreviewDefaults(),
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: "streaming", Directory: "~/projects/streaming", Profile: "work"},
			{Title: "quiet", Directory: "~/projects/quiet", Profile: "work"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊streaming", true, managedVars("work", "claude", "streaming", "~/projects/streaming")),
		uitestTab(2, 21, "🛑🦊quiet", false, managedVars("work", "claude", "quiet", "~/projects/quiet")),
	)}
	return uitestFixture{
		Name:        "paused_output",
		Description: "a working session whose scrollback is longer than the pane, numbered so following and holding are distinguishable on screen",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		Windows:     windows,
		Text:        numberedScrollback(40),
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-42 * time.Second).Unix()},
			21: {IconState: "stop"},
		},
	}
}

// numberedScrollback builds count lines of the form "output line NN". The
// zero-padded number is what makes a scenario's assertion name a POSITION in
// the buffer rather than a piece of content, so the same fixture serves the
// following pane and, later, the held one.
func numberedScrollback(count int) string {
	lines := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("output line %02d", i))
	}
	return strings.Join(lines, "\n") + "\n"
}

// healthStatesFixture is the health world no other fixture can produce. Every
// fixture without a Health block reports nothing checked, which reads as a
// daemon that is down — so "degraded" is already the hermetic default, and a
// fixture that only repeated it would prove nothing.
//
// This one says the opposite about the daemon and the harder thing about the
// hooks: the daemon is ALIVE, and the degradation is entirely in the hook
// files. That separates the two independent reasons the status bar says the
// same sentence, which is the distinction a per-issue health surface has to
// make and the flat sentence today cannot.
//
// All three hook categories are populated at once, and that is the point of the
// fixture rather than thoroughness for its own sake: in sync, drifted, and
// missing are not severities of one thing. A drifted file exists and says
// something stale; a missing one means the harness will never call amx at all.
// A surface that listed only a count could not be checked against a world where
// both are true of different profiles, so the world has to contain one.
func healthStatesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: uitestPreviewDefaults(),
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
			{Name: "archive", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/archive", Color: "purple"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "site", Directory: "~/projects/site", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🤖site", false, managedVars("personal", "codex", "site", "~/projects/site")),
	)}
	return uitestFixture{
		Name:        "health_states",
		Description: "a live daemon with hook files in all three states at once — one in sync, one drifted, one missing",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-42 * time.Second).Unix()},
			21: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-12 * time.Second).Unix()},
		},
		Health: &commands.HealthMsg{
			DaemonAlive: true,
			HookSummary: commands.HookHealth{
				InSync:  []string{"work"},
				Drifted: []string{"personal"},
				Missing: []string{"archive"},
			},
		},
	}
}

// attentionOverlapFixture is the fixture for rows that want the operator for
// MORE THAN ONE REASON AT ONCE. Today the dashboard shows one icon and one
// reason tag per row, so a row with two claims on the operator is
// indistinguishable from a row with one; that is exactly why the world has to
// exist before the surface that separates them.
//
// The overlaps:
//
//   - `blocked` is waiting on a permission prompt AND has an unread finished
//     turn behind it. Two independent facts, both of which say "you".
//   - `failed` has a terminal failure AND is unread — the pair an operator most
//     needs to see together, because a failure nobody has read is the one most
//     likely to be sitting there.
//   - `looping` is under an agent-session-loop run (the 🔁 marker in its title)
//     AND is asking an elicitation question AND is unread. Three at once, and
//     the loop marker is the one that is carried in the TITLE rather than in
//     daemon state, so it is the overlap a state-only reading would miss.
//
// `warned` is the discriminator and is not an overlap at all: a recoverable
// tool warning is NOT a claim on the operator. It stays in Active with the
// working rows. A fixture of nothing but attention rows would pass under a
// surface that had simply promoted everything, so the row that must NOT be
// promoted is what makes the others mean something. It carries a busy anchor
// for the same reason — it has to look like ordinary work, because it is.
//
// `unread` is the single-reason control: one claim, no overlap. Without it
// "two reasons" is not distinguishable from "any reason".
func attentionOverlapFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: uitestPreviewDefaults(),
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "blocked", Directory: "~/projects/blocked", Profile: "work"},
			{Title: "failed", Directory: "~/projects/failed", Profile: "personal"},
			{Title: "looping", Directory: "~/projects/looping", Profile: "work"},
			{Title: "unread", Directory: "~/projects/unread", Profile: "personal"},
			{Title: "warned", Directory: "~/projects/warned", Profile: "work"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🔑🦊blocked", true, managedVars("work", "claude", "blocked", "~/projects/blocked")),
		uitestTab(2, 21, "❗🤖failed", false, managedVars("personal", "codex", "failed", "~/projects/failed")),
		uitestTab(3, 31, "❓🔁🦊looping", false, managedVars("work", "claude", "looping", "~/projects/looping")),
		uitestTab(4, 41, "🛑🤖unread", false, managedVars("personal", "codex", "unread", "~/projects/unread")),
		uitestTab(5, 51, "⚠️🦊warned", false, managedVars("work", "claude", "warned", "~/projects/warned")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	return uitestFixture{
		Name:        "attention_overlap",
		Description: "rows carrying several reasons to want the operator at once — blocked-and-unread, failed-and-unread, looping-and-asking-and-unread — beside a tool warning that must stay Active",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "key", Reason: "permission", Unseen: true},
			21: {IconState: "error", Unseen: true},
			// Looping is set here and not only in the title. A non-nil States map
			// is authoritative for every field it owns, Looping included, so a
			// fixture that marked the loop in the Kitty title alone would have it
			// cleared out from under the row. The title keeps the marker anyway,
			// because that is what Kitty would really be showing.
			31: {IconState: "question", Reason: "request_user_input", Unseen: true, Looping: true},
			41: {IconState: "stop", Unseen: true},
			51: {IconState: "warning", BusySinceAt: busySince(42 * time.Second)},
		},
	}
}
