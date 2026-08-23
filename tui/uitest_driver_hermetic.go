package tui

// This file constructs the driver. Every seam the production model uses to
// reach the filesystem, the daemon, or agent-session-loop is replaced here with
// an offline stand-in, so a driver built without a fixture reproduces an
// unreachable daemon exactly and can never touch the operator's own state. The
// driver itself lives in uitest_driver.go.

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// uitestFrozenNow is the instant every harness run renders against. A
// seconds-resolution timer measured from the wall clock cannot be asserted
// exactly, so the driver pins the model's render clock here and fixtures
// anchor their daemon-owned timestamps at fixed offsets from it.
var uitestFrozenNow = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// NewUITestDriver builds a driver around a freshly constructed Model. The model
// is created from prepared config and an injected client (both supplied by the
// caller / Session 3), then sized with an initial tea.WindowSizeMsg.
func NewUITestDriver(cfg *config.Config, client kitty.KittyClient, size UITestSize) *UITestDriver {
	return NewUITestDriverModel(NewModel(cfg, client), size)
}

// NewUITestDriverModel wraps an existing Model — useful when a test has already
// loaded sessions or otherwise prepared state — and applies the initial size.
func NewUITestDriverModel(m Model, size UITestSize) *UITestDriver {
	d, err := newUITestDriverModel(m, size, "")
	if err != nil {
		panic(err)
	}
	return d
}

func newUITestDriverModel(m Model, size UITestSize, tempParent string) (*UITestDriver, error) {
	ctx, cancel := context.WithCancel(context.Background())
	snapshot, tempRoot, err := newHermeticUITestSnapshot(tempParent)
	if err != nil {
		cancel()
		return nil, err
	}
	m.transcriptIndex = snapshot.transcriptIndex
	m.transcriptReaders = snapshot.transcriptReaders
	m.sessionResults = append([]transcript.Result(nil), snapshot.transcriptResults...)
	m.plansIndex = snapshot.planIndex
	m.backgroundCommands = hermeticModelBackgroundCommands()
	m.now = func() time.Time { return uitestFrozenNow }
	// Plan fixtures exercise navigation and layout, not filesystem scan
	// latency. Supply deterministic command results so the driver never relies
	// on a 50ms wall-clock race or the operator's real plan index.
	m.planRefreshCommand = func(_ *plans.Index, _ *config.Config, seq int) tea.Cmd {
		return func() tea.Msg { return plansRefreshDoneMsg{seq: seq} }
	}
	m.planSearchCommand = func(seq int, index *plans.Index, query plans.Query) tea.Cmd {
		return func() tea.Msg {
			results, err := plans.Search(index, query)
			return planSearchDoneMsg{seq: seq, results: results, err: err}
		}
	}
	m.sessionWarmRefreshCommand = func(seq int, query transcript.Query) tea.Cmd {
		return func() tea.Msg {
			results, err := transcript.Search(snapshot.transcriptIndex, snapshot.transcriptReaders, query)
			return sessionWarmRefreshDoneMsg{seq: seq, results: results, searchErr: err}
		}
	}
	m.sessionRefreshCommand = func(seq int) tea.Cmd {
		return func() tea.Msg { return sessionsRefreshDoneMsg{seq: seq} }
	}
	m.sessionSearchCommand = func(seq int, query transcript.Query) tea.Cmd {
		return func() tea.Msg {
			results, err := transcript.Search(snapshot.transcriptIndex, snapshot.transcriptReaders, query)
			return sessionSearchDoneMsg{seq: seq, results: results, err: err}
		}
	}
	d := &UITestDriver{model: m, ctx: ctx, cancel: cancel, tempRoot: tempRoot}
	d.model.daemonCommand = func(daemon.Request) (daemon.Response, error) {
		return daemon.Response{}, d.liveAccess.reject("uitest: live daemon access is disabled")
	}
	if err := d.applySize(size); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// hermeticInitialFetch enumerates sessions from the no-op Kitty client and
// overlays the fixture's daemon-owned state, which no Kitty title can carry
// (the busy anchor above all). Both maps are nil for a driver built without a
// fixture, which reproduces an unreachable daemon exactly as before. The
// daemon is never contacted either way, so the no-live-daemon guarantee holds.
func hermeticInitialFetch(states map[int]daemon.WindowStateSnapshot, watchdog map[int]daemon.WatchdogState) func(Model, int) tea.Cmd {
	return func(m Model, _ int) tea.Cmd {
		return func() tea.Msg {
			windows, err := m.client.ListWindows()
			if err != nil {
				return errMsg{err: err}
			}
			sessions, _ := session.ListFromOSWindowsWithConfig(windows, m.cfg)
			if states != nil {
				applyDaemonStates(sessions, states)
			}
			return sessionsMsg{
				sessions: session.SortRunning(sessions, m.sortMode),
				watchdog: watchdog,
				states:   states,
			}
		}
	}
}

func hermeticModelBackgroundCommands() modelBackgroundCommands {
	return modelBackgroundCommands{
		initialFetch: hermeticInitialFetch(nil, nil),
		activeLoops: func(seq int) tea.Cmd {
			return func() tea.Msg { return activeLoopsMsg{seq: seq} }
		},
		health: func(_ Model, checkDaemon, checkHooks bool) tea.Cmd {
			return func() tea.Msg { return healthMsg{daemonChecked: checkDaemon, hookChecked: checkHooks} }
		},
		detail: func(windowID int) tea.Cmd {
			return func() tea.Msg {
				return detailEventsMsg{windowID: windowID, err: errors.New("uitest: live daemon access is disabled")}
			}
		},
		worktrees: func(_ Model, seq int) tea.Cmd {
			return func() tea.Msg { return worktreesLoadedMsg{seq: seq, entries: []actions.WorktreeView{}} }
		},
		worktreeAction: func(_ Model, id actions.ID, _ actions.Request) tea.Cmd {
			return func() tea.Msg {
				return worktreeActionDoneMsg{action: id, err: errors.New("uitest: live worktree action access is disabled")}
			}
		},
		aslSnapshot: func(_ Model, seq int) tea.Cmd {
			return func() tea.Msg {
				return aslSnapshotLoadedMsg{seq: seq, status: aslbridge.Status{Reason: "uitest: live agent-session-loop access is disabled"}}
			}
		},
		aslWorkbench: func(_ Model, rowID, _, focus string) tea.Cmd {
			return func() tea.Msg {
				return aslWorkbenchDoneMsg{rowID: rowID, focus: focus, err: errASLWorkbenchOpen}
			}
		},
		aslQuestion: func(_ Model, seq int, _, _ string) tea.Cmd {
			return func() tea.Msg {
				return aslQuestionLoadedMsg{seq: seq, err: errors.New("uitest: live agent-session-loop access is disabled")}
			}
		},
		aslAction: func(_ Model, seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
			return func() tea.Msg {
				return aslActionDoneMsg{
					seq: seq, control: control, rowID: rowID, method: req.Method,
					operationID: req.OperationID,
					err:         errors.New("uitest: live agent-session-loop access is disabled"),
				}
			}
		},
	}
}

// NewUITestConfigDriver stages an isolated environment under root, loads the
// config at srcConfigPath from inside it, and builds a driver backed by a no-op
// recording Kitty client reporting windows. setenv applies the isolation env
// vars (pass t.Setenv in tests, os.Setenv in the CLI). It returns the driver,
// the recording client, and the staged environment description so callers can
// assert that no real user paths are used.
func NewUITestConfigDriver(root, srcConfigPath string, windows []kitty.OSWindow, size UITestSize, setenv func(string, string) error) (*UITestDriver, *UITestKittyClient, UITestConfigEnv, error) {
	env, err := StageUITestConfigEnv(root, srcConfigPath, setenv)
	if err != nil {
		return nil, nil, UITestConfigEnv{}, err
	}
	cfg, err := config.Load(env.ConfigPath)
	if err != nil {
		return nil, nil, env, fmt.Errorf("uitest: loading staged config: %w", err)
	}
	client := NewUITestKittyClient(windows)
	d, err := newUITestDriverModel(NewModel(cfg, client), size, root)
	if err != nil {
		return nil, nil, env, err
	}
	d.loadSessionsFrom(client, cfg, nil, nil)
	return d, client, env, nil
}

// loadSessionsFrom populates the model's session list the way the production
// fetch command does, but synchronously: it enumerates managed sessions from
// client + cfg, overlays the caller's daemon-owned state, and feeds the result
// through Model.Update as a sessionsMsg. This lets a fixture- or config-backed
// driver start with a populated list without running the refresh ticker, and
// keeps that first list identical to what a later tick's hermetic initial fetch
// would produce. A query error leaves the model with an empty list rather than
// failing driver construction.
func (d *UITestDriver) loadSessionsFrom(client kitty.KittyClient, cfg *config.Config, states map[int]daemon.WindowStateSnapshot, watchdog map[int]daemon.WatchdogState) {
	sessions, _, err := session.ListWithConfig(client, cfg)
	if err != nil {
		return
	}
	if states != nil {
		applyDaemonStates(sessions, states)
	}
	d.recordErr(d.dispatch(sessionsMsg{
		sessions: session.SortRunning(sessions, d.model.sortMode),
		watchdog: watchdog,
		states:   states,
	}))
}
