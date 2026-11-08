//go:build tuitest

package tui

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// basicFixture is a realistic multi-profile dashboard: a couple of Claude
// projects under "work" and a Codex project under "personal".
func basicFixture() uitestFixture {
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
			{Title: "backend", Directory: "~/projects/backend", Profile: "work"},
			{Title: "dotfiles", Directory: "~/dotfiles", Profile: "personal"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "🦊backend", false, managedVars("work", "claude", "backend", "~/projects/backend")),
		uitestTab(3, 31, "🤖dotfiles", false, managedVars("personal", "codex", "dotfiles", "~/dotfiles")),
	)}
	return uitestFixture{
		Name:        "basic",
		Description: "two Claude work projects plus a Codex personal project",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
	}
}

// actionsFixture stresses the per-row actions overlay: a running session with a
// very long project title so the "Actions for <title>" header and box width are
// exercised.
func actionsFixture() uitestFixture {
	const longTitle = "monorepo-services-authentication-gateway"
	dir := "~/projects/" + longTitle
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: longTitle, Directory: dir, Profile: "work"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "🦊"+longTitle, true, managedVars("work", "claude", longTitle, dir)),
	)}
	return uitestFixture{
		Name:        "actions",
		Description: "single long-titled session for exercising the actions overlay width",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
	}
}

// aslQuestionFixture is the basic dashboard with one looping agent-session-loop
// run whose supervisor has handed a mailbox question to the user (❓🔁) and a
// sibling run still working (⚡🔁). It exists so scenarios can exercise the
// human-actionable presentation — attention grouping, the "awaiting response"
// gloss, and the `n`/`N` jump that replaced the retired `]q` chord — without a
// daemon painting the state.
func aslQuestionFixture() uitestFixture {
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
			{Title: "backend", Directory: "~/projects/backend", Profile: "work"},
			{Title: "dotfiles", Directory: "~/dotfiles", Profile: "personal"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "❓🔁🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🔁🦊backend", false, managedVars("work", "claude", "backend", "~/projects/backend")),
		uitestTab(3, 31, "🤖dotfiles", false, managedVars("personal", "codex", "dotfiles", "~/dotfiles")),
	)}
	return uitestFixture{
		Name:        "asl_question",
		Description: "an ASL supervisor awaiting a human mailbox reply alongside an active looping run",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		PaneGroups:  windows,
	}
}

// wideProfilesFixture stresses the profile (config) picker: several profiles
// with long names and long home directories so the picker box must grow to fit
// its widest row.
func wideProfilesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖", "copilot": "🧑"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green", "copilot": "purple"},
		Accounts: []config.Account{
			{Name: "claude-production-primary", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/claude-production-primary-workspace", Color: "blue"},
			{Name: "codex-experimental-sandbox", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/codex-experimental-sandbox-workspace", Color: "green"},
			{Name: "copilot-enterprise-review-board", Harness: "copilot", HomeDir: "~/.config/agent-mux/homes/copilot-enterprise-review-board-workspace", Color: "purple"},
		},
		Sessions: []config.Session{
			{Title: "platform-services-monorepo", Directory: "~/projects/platform-services-monorepo", Profile: "claude-production-primary"},
		},
	}
	windows := []fixturePaneGroup{uitestPaneGroup(
		uitestTab(1, 11, "🦊platform-services-monorepo", true,
			managedVars("claude-production-primary", "claude", "platform-services-monorepo", "~/projects/platform-services-monorepo")),
	)}
	return uitestFixture{
		Name:        "wide_profiles",
		Description: "long profile names and home dirs for exercising the profile picker width",
		Size:        UITestSize{Width: 140, Height: 36},
		Config:      cfg,
		PaneGroups:  windows,
	}
}

// portableTmuxFixture has three sessions hosted by a private tmux server.
// One needs the operator, one is working, and one is at rest.
func portableTmuxFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.claude-work", Color: "blue"},
			{Name: "codex-work", Harness: "codex", HomeDir: "~/.codex-work", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "web", Directory: "~/projects/web", Profile: "work"},
			{Title: "docs", Directory: "~/projects/docs", Profile: "codex-work"},
		},
	}
	row := func(n int, project, profile, harness string) session.RunningSession {
		id := commands.PortableIDBase + n
		return session.RunningSession{
			TabID: id, WindowID: id, Title: project, Base: project, Project: project,
			Profile: profile, Account: profile, Harness: harness, Directory: "~/projects/" + project,
			Backend: runtimesession.BackendTmux,
			Group:   backend.GroupID(fmt.Sprintf("$%d", n)), Pane: backend.PaneID(fmt.Sprintf("%%%d", n)),
			RuntimeSessionID: runtimesession.ID(fmt.Sprintf("rs-%032d", n)),
		}
	}
	busySince := uitestFrozenNow.Add(-95 * time.Second).Unix()
	return uitestFixture{
		Name:        "portable_tmux",
		Description: "three tmux-hosted sessions in a stock terminal",
		Size:        UITestSize{Width: 140, Height: 36},
		Config:      cfg,
		Portable:    []session.RunningSession{row(0, "api", "work", "claude"), row(1, "web", "work", "claude"), row(2, "docs", "codex-work", "codex")},
		States: map[int]protocol.WindowStateSnapshot{
			commands.PortableIDBase:     {IconState: "key"},
			commands.PortableIDBase + 1: {IconState: "bolt", BusySinceAt: busySince},
		},
		Text: "tmux pane output line 1\ntmux pane output line 2\n",
	}
}
