//go:build tuitest

package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
)

// This file constructs the driver. Every seam the production model uses to
// reach the filesystem, the daemon, or agent-session-loop is replaced here with
// an offline stand-in, so a driver built without a fixture reproduces an
// unreachable daemon exactly and can never touch the operator's own state. The
// driver itself lives in uitest_driver.go.

// uitestFrozenNow is the instant every harness run renders against. A
// seconds-resolution timer measured from the wall clock cannot be asserted
// exactly, so the driver pins the model's render clock here and fixtures
// anchor their daemon-owned timestamps at fixed offsets from it.
var uitestFrozenNow = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// newUITestDriver builds a driver around a freshly constructed Model. The model
// is created from prepared config and an injected client (both supplied by the
// caller / Session 3), then sized with an initial tea.WindowSizeMsg.
//
// It defaults the config's Preview block on the way in. A driver stands in for
// a running program, and a running program's config came through config.Load;
// a fixture's did not, so without this every scenario would be judged against a
// split geometry no installation produces.
func newUITestDriver(cfg *config.Config, client any, size UITestSize) *uitestDriver {
	if cfg != nil {
		config.ApplyPreviewDefaults(&cfg.Defaults.Preview)
	}
	return newUITestDriverModel(NewModel(cfg, client), size)
}

// newUITestDriverModel wraps an existing Model — useful when a test has already
// loaded sessions or otherwise prepared state — and applies the initial size.
func newUITestDriverModel(m Model, size UITestSize) *uitestDriver {
	d, err := newUITestDriverUnder(m, size, "")
	if err != nil {
		panic(err)
	}
	return d
}

func newUITestDriverUnder(m Model, size UITestSize, tempParent string) (*uitestDriver, error) {
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
	m.flightRead = fixtureFlightReader(nil)
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
	// The run form's submission would ask the operator's agent-session-loop
	// about a plan and could launch an agent. The harness answers every one
	// as a plan that is not ready, which is also the form's tallest state.
	m.planRunCommand = func(seq int, plan plans.PlanMeta, _ map[string]string) tea.Cmd {
		return func() tea.Msg {
			readiness := planrun.Readiness{PlanDir: plan.DirPath, State: planrun.StateNotReady, Findings: []planrun.Finding{{
				Severity: "yellow", Category: "manifest-readiness",
				Message: "plan readiness state is 'reviewed'", Detail: "run preflight to advance to loop-ready when checks pass",
			}}}
			return planRunDoneMsg{
				seq:    seq,
				result: actions.Result{Action: actions.PlanRun, Data: map[string]any{actions.DataPlanReadiness: readiness}},
				err:    &actions.Error{Code: "plan_not_ready", Action: actions.PlanRun, Message: readiness.Summary()},
			}
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
	d := &uitestDriver{model: m, ctx: ctx, cancel: cancel, tempRoot: tempRoot}
	d.model.daemonCommand = func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{}, d.liveAccess.reject("uitest: live daemon access is disabled")
	}
	if err := d.applySize(size); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// hermeticPortableRefresh serves a fixed tmux session listing with the
// fixture's daemon state and project-family facts. It preserves the refresh
// sequence so the model's one-refresh-in-flight controller settles normally.
func hermeticPortableRefresh(overlay uitestSessionOverlay, portable []session.RunningSession) func(Model, int, int) tea.Cmd {
	return func(m Model, seq, _ int) tea.Cmd {
		return func() tea.Msg {
			sessions := append([]session.RunningSession(nil), portable...)
			overlay.apply(sessions)
			return commands.SessionsMsg{
				Seq:      seq,
				Sessions: session.SortRunning(sessions, m.sortMode),
				Watchdog: overlay.Watchdog,
				States:   overlay.States,
				Topology: commands.TopologyFreshness{
					Source:     commands.TopologyFromDirect,
					CapturedAt: m.now(),
				},
			}
		}
	}
}

func hermeticModelBackgroundCommands() modelBackgroundCommands {
	return modelBackgroundCommands{
		topologyRefresh: hermeticPortableRefresh(uitestSessionOverlay{}, nil),
		health: func(_ Model, checkDaemon, checkHooks bool) tea.Cmd {
			return func() tea.Msg { return commands.HealthMsg{DaemonChecked: checkDaemon, HookChecked: checkHooks} }
		},
		detail: func(windowID int) tea.Cmd {
			return func() tea.Msg {
				return commands.DetailEventsMsg{WindowID: windowID, Err: errors.New("uitest: live daemon access is disabled")}
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

// newUITestConfigDriver stages an isolated environment under root, loads the
// config at srcConfigPath from inside it, and builds a driver with tmux panes.
// setenv applies the isolation env
// vars (pass t.Setenv in tests, os.Setenv in the CLI). It returns the driver,
// the recording client, and the staged environment description so callers can
// assert that no real user paths are used.
func newUITestConfigDriver(root, srcConfigPath string, panes []backend.Pane, size UITestSize, setenv func(string, string) error) (*uitestDriver, uitestConfigEnv, error) {
	env, err := stageUITestConfigEnv(root, srcConfigPath, setenv)
	if err != nil {
		return nil, uitestConfigEnv{}, err
	}
	cfg, err := config.Load(env.ConfigPath)
	if err != nil {
		return nil, env, fmt.Errorf("uitest: loading staged config: %w", err)
	}
	d, err := newUITestDriverUnder(NewModel(cfg, nil), size, root)
	if err != nil {
		return nil, env, err
	}
	rows := session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg)
	d.model.backgroundCommands.topologyRefresh = hermeticPortableRefresh(uitestSessionOverlay{}, rows)
	d.loadSessionsWith(uitestSessionOverlay{}, rows)
	return d, env, nil
}

// loadSessionsFrom populates the model's session list the way the production
// fetch command does, but synchronously: it enumerates managed sessions from
// client + cfg, applies the caller's overlay, and feeds the result through
// Model.Update as a commands.SessionsMsg. This lets a fixture- or config-backed
// driver start with a populated list without running the refresh ticker, and
// keeps that first list identical to what a later tick's hermetic initial fetch
// would produce — which is why both go through the same overlay. A query error
// leaves the model with an empty list rather than failing driver construction.
func (d *uitestDriver) loadSessionsWith(overlay uitestSessionOverlay, rows []session.RunningSession) {
	sessions := append([]session.RunningSession(nil), rows...)
	overlay.apply(sessions)
	// This load stands in for the startup refresh the constructor armed, so it
	// carries that refresh's sequence: settling it is what lets the first tick
	// start a refresh instead of collapsing into the pending bit.
	d.recordErr(d.dispatch(commands.SessionsMsg{
		Seq:      d.model.topologyRefreshActiveSeq,
		Sessions: session.SortRunning(sessions, d.model.sortMode),
		Watchdog: overlay.Watchdog,
		States:   overlay.States,
		Topology: commands.TopologyFreshness{
			Source:     commands.TopologyFromDirect,
			CapturedAt: d.model.now(),
		},
	}))
}
