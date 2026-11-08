//go:build tuitest

package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// worktreeBranchesFixture is the case managed worktrees make ordinary: three
// rows sitting in ONE repository and differing only by checkout, plus a fourth
// that is not a repository at all. It is the fixture for the branch because it
// is the situation a branch exists to resolve.
//
// Two of the rows are worktrees of the same project. Their directories share
// `~/projects/worktrees/api-` and diverge in the tail, which is exactly what the
// strip's left truncation surrenders first at a narrow width — so without a
// branch the two are indistinguishable at the sizes an operator actually runs
// the dashboard at.
//
// Since the queue redesign the branch is the INSPECTOR's and not the row's: it
// names the selected checkout underneath the queue rather than repeating itself
// down a column, and the rows themselves are disambiguated by name instead. The
// cases the fixture carries survive that move:
//
//   - a dirty checkout (`main*`), which is the whole of the dirty rendering: a
//     trailing marker on the branch rather than a color or a second field, so it
//     survives a monochrome terminal and the harness's own stripAnsi;
//   - a NON-REPOSITORY row, whose strip is the bare directory rather than a
//     leading separator with nothing in front of it;
//   - an activity timer on every row, so a branch and a timer are seen to be
//     different facts about the same selection.
//
// The branch strings are written as bare values for the reason the blocked
// reason kinds are: a fixture stands in for one daemon state query, whose Branch
// and Dirty fields are what internal/gitstate last read off disk. Whether a
// non-repository, a disabled probe, a timeout, or a detached HEAD produced the
// empty branch is deliberately not representable here, because it is not
// representable on the wire either — gitstate collapses all four to the same
// nothing.
func worktreeBranchesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "api-feat-x", Directory: "~/projects/worktrees/api-feat-x", Profile: "work"},
			{Title: "api-hotfix", Directory: "~/projects/worktrees/api-hotfix", Profile: "work"},
			{Title: "notes", Directory: "~/notes", Profile: "personal"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "⚡🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🦊api-feat-x", false, managedVars("work", "claude", "api-feat-x", "~/projects/worktrees/api-feat-x")),
		uitestTab(3, 31, "⚡🦊api-hotfix", false, managedVars("work", "claude", "api-hotfix", "~/projects/worktrees/api-hotfix")),
		uitestTab(4, 41, "⚡🤖notes", false, managedVars("personal", "codex", "notes", "~/notes")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	return uitestFixture{
		Name:        "worktree_branches",
		Description: "three rows in one repository — a dirty main checkout and two managed worktrees — plus a directory that is not a repository at all",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(42 * time.Second), Branch: "main", Dirty: true},
			21: {IconState: "bolt", BusySinceAt: busySince(3*time.Minute + 7*time.Second), Branch: "amx/feat-x"},
			31: {IconState: "bolt", BusySinceAt: busySince(12 * time.Second), Branch: "amx/hotfix"},
			41: {IconState: "bolt", BusySinceAt: busySince(1*time.Minute + 5*time.Second)},
		},
	}
}

// projectFamiliesFixture is the Projects view's world: seven rows arranged into
// two multi-checkout repository families, one single-checkout family, and one
// row belonging to no family at all. It is the fixture the tree scenarios need,
// and it is the reason uitestFixture.Families exists — the real stamp comes from
// a Git probe against a directory on disk, so without an injection seam a
// multi-checkout family could only be built out of temporary repositories whose
// paths would then appear in the very columns the scenarios assert on.
//
// The four grouping cases it has to hold at once:
//
//   - `api`, three checkouts of one repository whose members sit in three
//     DIFFERENT urgency buckets. That is what makes the summary a claim rather
//     than a label: it speaks with its most urgent member's voice (🔑), and
//     folding it must not change the status line's tally, because the tally
//     counts sessions and a summary is a line;
//   - `docs`, two checkouts under a different profile and harness, so a family
//     is visibly not the same thing as a profile;
//   - `notes`, a family with exactly ONE member, which earns no summary. A
//     summary over a lone session would be decoration;
//   - `scratch`, a row with no family at all, which renders as a top-level leaf
//     indistinguishable from the singleton above it. The two arrive at the same
//     rendering by different routes, and pinning both is what stops a later
//     change from special-casing one of them.
//
// The bucket spread is deliberate and load-bearing for the count scenarios:
// two attention (a 🔑 prompt and an unread 🛑), two active, three idle. Both
// attention rows are in different families, so `!attention` leaves a family
// filtered down to one visible member — the case where a summary correctly
// disappears rather than claiming a count nobody can reach.
func projectFamiliesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			// Spelled out because a fixture builds its Config literally and never
			// runs config.Load's defaulting pass: a zero Preview block has ratio
			// 0, which every split calculation reads as "no preview fits". These
			// are exactly the values Load would have filled in, so the summary's
			// project card is reachable from a scenario.
			Preview: config.PreviewConfig{
				Direction: "horizontal", Ratio: 0.5, RefreshMs: 500, MinCols: 100, MinRows: 30,
			},
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "api-feat-x", Directory: "~/projects/worktrees/api-feat-x", Profile: "work"},
			{Title: "api-hotfix", Directory: "~/projects/worktrees/api-hotfix", Profile: "work"},
			{Title: "docs", Directory: "~/projects/docs", Profile: "personal"},
			{Title: "docs-review", Directory: "~/projects/worktrees/docs-review", Profile: "personal"},
			{Title: "notes", Directory: "~/notes", Profile: "personal"},
			{Title: "scratch", Directory: "~/scratch", Profile: "work"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "🔑🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🦊api-feat-x", false, managedVars("work", "claude", "api-feat-x", "~/projects/worktrees/api-feat-x")),
		uitestTab(3, 31, "🛑🦊api-hotfix", false, managedVars("work", "claude", "api-hotfix", "~/projects/worktrees/api-hotfix")),
		uitestTab(4, 41, "⚡🤖docs", false, managedVars("personal", "codex", "docs", "~/projects/docs")),
		uitestTab(5, 51, "🛑🤖docs-review", false, managedVars("personal", "codex", "docs-review", "~/projects/worktrees/docs-review")),
		uitestTab(6, 61, "🛑🤖notes", false, managedVars("personal", "codex", "notes", "~/notes")),
		uitestTab(7, 71, "🛑🦊scratch", false, managedVars("work", "claude", "scratch", "~/scratch")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	// The keys are opaque to everything downstream — the dashboard groups by
	// them and must never render them — so they are written as short readable
	// strings rather than as the canonical common-dir paths discovery mints.
	// A scenario that depended on their shape would be pinning the resolver,
	// which is tested against real temporary repositories in internal/session.
	return uitestFixture{
		Name:        "project_families",
		Description: "two multi-checkout repository families spanning three urgency buckets, plus a single-checkout family and an unfamilied row",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "key", Reason: "permission", Branch: "main", Dirty: true},
			21: {IconState: "bolt", BusySinceAt: busySince(3*time.Minute + 7*time.Second), Branch: "amx/feat-x"},
			31: {IconState: "stop", Branch: "amx/hotfix"},
			41: {IconState: "bolt", BusySinceAt: busySince(42 * time.Second), Branch: "main"},
			51: {IconState: "stop", Branch: "amx/review"},
			61: {IconState: "stop", Unseen: true},
			71: {IconState: "stop"},
		},
		Families: map[int]uitestFamily{
			11: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			21: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			31: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			41: {Key: "git:docs", Label: "docs", IsRepository: true, PrimaryRoot: "~/projects/docs"},
			51: {Key: "git:docs", Label: "docs", IsRepository: true, PrimaryRoot: "~/projects/docs"},
			61: {Key: "project:notes", Label: "notes", PrimaryRoot: "~/notes"},
			// 71 (scratch) is deliberately absent: an authoritative Families map
			// clears what it does not name, which is the only way to say that a
			// discovered row belongs to no family.
		},
	}
}

// triageIntegrationFixture is the release-level dashboard world. Unlike the
// narrower fixtures above, it deliberately composes every triage input in one
// place: every group/subtype rank, zero/equal/distinct presentation clocks,
// case-folded project identity across profiles and repository families, a label
// that needs quoted filter syntax, and enough canonical labels to make the
// project switcher scroll in a short terminal.
func triageIntegrationFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: config.PreviewConfig{
				Direction: "horizontal", Ratio: 0.4, RefreshMs: 500, MinCols: 100, MinRows: 20,
			},
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "release-guard", Directory: "~/projects/release-guard", Profile: "work"},
			{Title: "portal-work", Directory: "~/projects/portal-work", Profile: "work"},
			{Title: "portal-personal", Directory: "~/projects/portal-personal", Profile: "personal"},
			{Title: "finished-work", Directory: "~/projects/finished-work", Profile: "personal"},
			{Title: "watchtower", Directory: "~/projects/watchtower", Profile: "work"},
			{Title: "build-farm", Directory: "~/projects/build-farm", Profile: "work"},
			{Title: "agent-fleet", Directory: "~/projects/agent-fleet", Profile: "personal"},
			{Title: "compact-ops", Directory: "~/projects/compact-ops", Profile: "personal"},
			{Title: "quiet-queue", Directory: "~/projects/quiet-queue", Profile: "work"},
			{Title: "archive", Directory: "~/projects/archive", Profile: "personal"},
			{Title: "legacy-lab", Directory: "~/projects/legacy-lab", Profile: "work"},
			{Title: "spare-parts", Directory: "~/projects/spare-parts", Profile: "personal"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "❗🦊release-guard", true, managedVars("work", "claude", "release-guard", "~/projects/release-guard")),
		uitestTab(2, 21, "🔑🦊portal-work", false, managedVars("work", "claude", "portal-work", "~/projects/portal-work")),
		uitestTab(3, 31, "❓🤖portal-personal", false, managedVars("personal", "codex", "portal-personal", "~/projects/portal-personal")),
		uitestTab(4, 41, "🛑🤖finished-work", false, managedVars("personal", "codex", "finished-work", "~/projects/finished-work")),
		uitestTab(5, 51, "⚠️🦊watchtower", false, managedVars("work", "claude", "watchtower", "~/projects/watchtower")),
		uitestTab(6, 61, "⚡🦊build-farm", false, managedVars("work", "claude", "build-farm", "~/projects/build-farm")),
		uitestTab(7, 71, "🤖🤖agent-fleet", false, managedVars("personal", "codex", "agent-fleet", "~/projects/agent-fleet")),
		uitestTab(8, 81, "🌀🤖compact-ops", false, managedVars("personal", "codex", "compact-ops", "~/projects/compact-ops")),
		uitestTab(9, 91, "🦊quiet-queue", false, managedVars("work", "claude", "quiet-queue", "~/projects/quiet-queue")),
		uitestTab(10, 101, "🛑🤖archive", false, managedVars("personal", "codex", "archive", "~/projects/archive")),
		uitestTab(11, 111, "🦊legacy-lab", false, managedVars("work", "claude", "legacy-lab", "~/projects/legacy-lab")),
		uitestTab(12, 121, "🛑🤖spare-parts", false, managedVars("personal", "codex", "spare-parts", "~/projects/spare-parts")),
	)}
	changed := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).UnixMilli() }
	return uitestFixture{
		Name:        "triage_integration",
		Description: "all triage ranks and presentation-clock tie cases across a scrollable case-folded project scope set",
		Size:        UITestSize{Width: 140, Height: 16},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11:  {IconState: "error", PresentationChangedAtMS: changed(1 * time.Second)},
			21:  {IconState: "key", Reason: "permission", PresentationChangedAtMS: changed(2 * time.Second)},
			31:  {IconState: "question", Reason: "request_user_input", PresentationChangedAtMS: changed(3 * time.Second)},
			41:  {IconState: "stop", Unseen: true, PresentationChangedAtMS: changed(4 * time.Second)},
			51:  {IconState: "warning", PresentationChangedAtMS: changed(5 * time.Second)},
			61:  {IconState: "bolt", PresentationChangedAtMS: changed(6 * time.Second)},
			71:  {IconState: "subagent", PresentationChangedAtMS: changed(6 * time.Second)},
			81:  {IconState: "compact"},
			91:  {},
			101: {IconState: "stop", PresentationChangedAtMS: changed(7 * time.Second)},
			111: {IconState: "legacy", PresentationChangedAtMS: changed(8 * time.Second)},
			121: {IconState: "stop"},
		},
		Families: map[int]uitestFamily{
			11:  {Key: "git:release", Label: "Release Guard", IsRepository: true, PrimaryRoot: "~/projects/release-guard"},
			21:  {Key: "git:portal-work", Label: "Client Portal", IsRepository: true, PrimaryRoot: "~/projects/portal-work"},
			31:  {Key: "git:portal-personal", Label: "client portal", IsRepository: true, PrimaryRoot: "~/projects/portal-personal"},
			41:  {Key: "git:finished", Label: "Finished Work", IsRepository: true, PrimaryRoot: "~/projects/finished-work"},
			51:  {Key: "git:watchtower", Label: "Watchtower", IsRepository: true, PrimaryRoot: "~/projects/watchtower"},
			61:  {Key: "git:build", Label: "Build Farm", IsRepository: true, PrimaryRoot: "~/projects/build-farm"},
			71:  {Key: "git:agents", Label: "Agent Fleet", IsRepository: true, PrimaryRoot: "~/projects/agent-fleet"},
			81:  {Key: "git:compact", Label: "Compact Ops", IsRepository: true, PrimaryRoot: "~/projects/compact-ops"},
			91:  {Key: "git:quiet", Label: "Quiet Queue", IsRepository: true, PrimaryRoot: "~/projects/quiet-queue"},
			101: {Key: "git:archive", Label: "Archive", IsRepository: true, PrimaryRoot: "~/projects/archive"},
			111: {Key: "git:legacy", Label: "Legacy Lab", IsRepository: true, PrimaryRoot: "~/projects/legacy-lab"},
			121: {Key: "git:spare", Label: "Spare Parts", IsRepository: true, PrimaryRoot: "~/projects/spare-parts"},
		},
	}
}

// triageVerticalFixture keeps the same integrated world and changes only the
// configured preview direction, which the dashboard no longer reads: the
// inspector is a side-by-side split or it is full screen, and there is no
// stacked form for the setting to select. The fixture earns its place by
// proving exactly that — same data, `direction = "vertical"`, and a screen the
// horizontal scenarios would recognize.
//
// The setting is not dead, which is why this is a claim about the dashboard
// rather than about the configuration: it still governs the launcher's own
// preview pane.
func triageVerticalFixture() uitestFixture {
	fx := triageIntegrationFixture()
	fx.Name = "triage_vertical"
	fx.Description = "triage_integration with the preview configured below the list"
	fx.Config.Defaults.Preview.Direction = "vertical"
	return fx
}

// familyUnreadFixture is the one world the unread-completion marker cannot be
// proved in anywhere else: an unseen stop sitting INSIDE a multi-checkout
// family, where folding the family is what decides whether the news is said on
// the summary or on the leaf.
//
// It is a separate fixture rather than an edit to project_families on purpose.
// That fixture's ordering, bucket spread, and count matrix are pinned by nine
// scenarios and read as a single load-bearing claim; adding a second unread
// member to it would have moved counts those scenarios assert exactly, and
// relaxing them to accommodate the marker would have spent grouping evidence to
// buy marker evidence. This fixture buys the marker evidence separately.
//
// The six rows are chosen so every way the marker can go wrong is visible in
// one screen:
//
//   - `svc`, a THREE-member family holding the unread stop (`svc-review`)
//     beside a SEEN stop (`svc-legacy`) and an active checkout (`svc`). The seen
//     sibling is the control: a marker driven by the stop state alone rather
//     than by the unseen bit would mark it too, and a collapsed summary that
//     aggregated "any stopped member" rather than "any unread member" would
//     still light up after the unread one was read;
//   - `web`, a two-member family with NO unread member at all, whose summary
//     must stay unmarked in both folds. Without it, a summary that marked every
//     family would pass every assertion `svc` can make;
//   - `tmp`, an unfamilied seen stop, which renders as a top-level leaf and
//     charges the gutter nothing.
//
// Every row is a stop or an active row and none carries a busy anchor that
// would pulse, so the gutter's contents are decided by the unseen bit alone:
// the pulse arm of the notice rule cannot fire here and cannot be mistaken for
// the dot. The surrounding rows are what make an alignment regression legible —
// five unmarked rows share the gutter with the marked one, so a marker rendered
// inside the row content instead of in front of it shifts exactly one line out
// of six.
func familyUnreadFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "svc", Directory: "~/projects/svc", Profile: "work"},
			{Title: "svc-legacy", Directory: "~/projects/worktrees/svc-legacy", Profile: "work"},
			{Title: "svc-review", Directory: "~/projects/worktrees/svc-review", Profile: "work"},
			{Title: "web", Directory: "~/projects/web", Profile: "personal"},
			{Title: "web-beta", Directory: "~/projects/worktrees/web-beta", Profile: "personal"},
			{Title: "tmp", Directory: "~/tmp", Profile: "personal"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "⚡🦊svc", true, managedVars("work", "claude", "svc", "~/projects/svc")),
		uitestTab(2, 21, "🛑🦊svc-legacy", false, managedVars("work", "claude", "svc-legacy", "~/projects/worktrees/svc-legacy")),
		uitestTab(3, 31, "🛑🦊svc-review", false, managedVars("work", "claude", "svc-review", "~/projects/worktrees/svc-review")),
		uitestTab(4, 41, "⚡🤖web", false, managedVars("personal", "codex", "web", "~/projects/web")),
		uitestTab(5, 51, "🛑🤖web-beta", false, managedVars("personal", "codex", "web-beta", "~/projects/worktrees/web-beta")),
		uitestTab(6, 61, "🛑🤖tmp", false, managedVars("personal", "codex", "tmp", "~/tmp")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	return uitestFixture{
		Name:        "family_unread",
		Description: "an unread stop folded inside a three-checkout family, beside a seen sibling, a family with nothing unread, and an unfamilied leaf",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(1*time.Minute + 12*time.Second), Branch: "main"},
			21: {IconState: "stop", Branch: "amx/legacy"},
			31: {IconState: "stop", Unseen: true, Branch: "amx/review"},
			41: {IconState: "bolt", BusySinceAt: busySince(24 * time.Second), Branch: "main"},
			51: {IconState: "stop", Branch: "amx/beta"},
			61: {IconState: "stop"},
		},
		Families: map[int]uitestFamily{
			11: {Key: "git:svc", Label: "svc", IsRepository: true, PrimaryRoot: "~/projects/svc"},
			21: {Key: "git:svc", Label: "svc", IsRepository: true, PrimaryRoot: "~/projects/svc"},
			31: {Key: "git:svc", Label: "svc", IsRepository: true, PrimaryRoot: "~/projects/svc"},
			41: {Key: "git:web", Label: "web", IsRepository: true, PrimaryRoot: "~/projects/web"},
			51: {Key: "git:web", Label: "web", IsRepository: true, PrimaryRoot: "~/projects/web"},
			// 61 (tmp) is deliberately absent, exactly as scratch is in
			// project_families: an authoritative Families map clears what it does
			// not name, which is the only way to say a row belongs to no family.
		},
	}
}
