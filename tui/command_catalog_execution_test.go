package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

func TestCommandRunRejectsDisabledAndHiddenCommands(t *testing.T) {
	m, _, _ := commandTestModel(session.RunningSession{
		TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work", Locked: true,
	})
	ctx := captureCommandContext(m)
	before := m
	after, cmd := commandByID(t, commands.RestartSession).Run(m, ctx)
	if cmd != nil || after.mode != before.mode || after.confirmAction != "" {
		t.Fatalf("disabled restart mutated model: mode=%v action=%q cmd=%v", after.mode, after.confirmAction, cmd != nil)
	}
	after, cmd = commandByID(t, commands.ClearFilters).Run(m, ctx)
	if cmd != nil || !after.activeFilter.IsEmpty() {
		t.Fatal("hidden clear-filters command should be a no-op")
	}
}

func TestCommandExecutionUsesCapturedTarget(t *testing.T) {
	client := newUITestKittyClient(nil)
	m := testModelWithSessions()
	m.client = client
	m = loadSessions(m, []session.RunningSession{
		{TabID: 11, WindowID: 101, Base: "api", Project: "api", Profile: "work"},
		{TabID: 22, WindowID: 202, Base: "backend", Project: "backend", Profile: "work"},
	})
	m.cursor = 1
	ctx := captureCommandContext(m)
	m.cursor = 0 // A refresh/controller move after capture must not retarget.
	_, cmd := commandByID(t, commands.FocusSession).Run(m, ctx)
	if cmd == nil {
		t.Fatal("focus returned nil command")
	}
	_ = cmd()
	calls := client.CallsFor("FocusTab")
	if len(calls) != 1 || calls[0].Match != kitty.MatchTabByID(22) {
		t.Fatalf("focus calls = %+v, want captured tab 22", calls)
	}
}

func TestCommandRowSideEffects(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	cases := []struct {
		name      string
		id        commands.ID
		wantOp    string
		wantMatch string
		wantVar   string
	}{
		{"focus", commands.FocusSession, "FocusTab", kitty.MatchTabByID(22), ""},
		{"peek", commands.PeekScrollback, "LaunchOverlayPager", kitty.MatchWindowByID(202), ""},
		{"lock", commands.ToggleLockID, "SetUserVars", kitty.MatchWindowByID(202), "amx_locked"},
		{"mute", commands.ToggleMuteID, "SetUserVars", kitty.MatchWindowByID(202), "cc_mute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, client := commandTestModel(target)
			_, cmd := commandByID(t, tc.id).Run(m, ctx)
			if cmd == nil {
				t.Fatal("execution returned nil command")
			}
			_ = cmd()
			calls := client.CallsFor(tc.wantOp)
			if len(calls) != 1 || calls[0].Match != tc.wantMatch {
				t.Fatalf("%s calls = %+v, want one call matching %q", tc.wantOp, calls, tc.wantMatch)
			}
			if tc.wantVar != "" {
				if _, ok := calls[0].Vars[tc.wantVar]; !ok {
					t.Fatalf("%s vars = %v, want %q", tc.wantOp, calls[0].Vars, tc.wantVar)
				}
			}
		})
	}
}

func TestCommandCopyAndClearStatusEffects(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work", State: "key",
	}
	m, ctx, _ := commandTestModel(target)

	originalCopy := commands.CopyToClipboard
	defer func() { commands.CopyToClipboard = originalCopy }()
	var copied string
	commands.CopyToClipboard = func(text string) error {
		copied = text
		return nil
	}
	_, copyCmd := commandByID(t, commands.CopyTitleID).Run(m, ctx)
	if copyCmd == nil {
		t.Fatal("copy title returned nil command")
	}
	_ = copyCmd()
	if copied != "api" {
		t.Fatalf("copied title = %q, want api", copied)
	}

	var request protocol.Request
	m.daemonCommand = func(req protocol.Request) (protocol.Response, error) {
		request = req
		return protocol.Response{OK: true}, nil
	}
	_, clearCmd := commandByID(t, commands.ClearStatusIcon).Run(m, ctx)
	if clearCmd == nil {
		t.Fatal("clear status returned nil command")
	}
	drainMsgs(t, clearCmd)
	if request.Action != "user_clear" || request.WindowID != 202 {
		t.Fatalf("clear-status request = %+v, want user_clear for window 202", request)
	}
}

func TestCommandConfiguredProjectExecutions(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work",
	}
	for _, id := range []commands.ID{commands.OpenDirectoryID, commands.OpenEditorID} {
		t.Run(string(id), func(t *testing.T) {
			m, ctx, _ := commandTestModel(target)
			got, cmd := commandByID(t, id).Run(m, ctx)
			if cmd == nil {
				t.Fatal("configured project execution returned nil command")
			}
			if got.targetSession.TabID != target.TabID {
				t.Fatalf("target tab = %d, want captured tab %d", got.targetSession.TabID, target.TabID)
			}
		})
	}
}

func TestCommandRowTransitions(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work", State: "bolt",
	}
	type transitionCase struct {
		name  string
		id    commands.ID
		setup func(*Model)
		check func(*testing.T, Model, tea.Cmd)
	}
	cases := []transitionCase{
		{
			// Restart honors confirm_destructive from the palette exactly as it
			// does from the per-row menu. It used to confirm regardless of the
			// preference, which is the drift the shared controller removed.
			name: "restart confirms when asked to", id: commands.RestartSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = true },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfirm || got.confirmAction != "restart" || got.confirmConsequence != wantRestartConsequence || got.confirmReturnMode != ModeList || cmd != nil {
					t.Fatalf("restart result: mode=%v action=%q consequence=%q return=%v cmd=%v", got.mode, got.confirmAction, got.confirmConsequence, got.confirmReturnMode, cmd != nil)
				}
			},
		},
		{
			name: "override profile picker", id: commands.OverrideProfile,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfigPicker || got.configPickerPurpose != launch.ConfigPickerRestartProfile || got.configPickerReturnMode != ModeList || got.configPickerCurrent != "work" || got.configPickerProjectIdx != -1 || cmd != nil {
					t.Fatalf("override result: mode=%v purpose=%v return=%v current=%q project=%d cmd=%v", got.mode, got.configPickerPurpose, got.configPickerReturnMode, got.configPickerCurrent, got.configPickerProjectIdx, cmd != nil)
				}
			},
		},
		{
			name: "launch another picker", id: commands.LaunchAnother,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfigPicker || got.configPickerPurpose != launch.ConfigPickerDashboardLaunch || got.configPickerReturnMode != ModeList || got.configPickerCurrent != "work" || cmd != nil {
					t.Fatalf("launch result: mode=%v purpose=%v return=%v current=%q cmd=%v", got.mode, got.configPickerPurpose, got.configPickerReturnMode, got.configPickerCurrent, cmd != nil)
				}
			},
		},
		{
			name: "close without confirmation", id: commands.CloseSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = false },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				// The recovery is reserved, not offered: nothing is undoable
				// until the close reports that the tab was observed to go.
				if got.mode != ModeList || cmd == nil || len(got.activeUndoEntries()) != 0 || len(got.toasts) != 1 || !strings.Contains(got.toasts[0].Text, "closing") {
					t.Fatalf("close result: mode=%v cmd=%v undo=%d toasts=%v", got.mode, cmd != nil, len(got.activeUndoEntries()), got.toasts)
				}
			},
		},
		{
			name: "close with confirmation", id: commands.CloseSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = true },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfirm || got.confirmAction != "close" || got.confirmConsequence != wantCloseConsequence || got.confirmReturnMode != ModeList || cmd != nil || len(got.activeUndoEntries()) != 0 {
					t.Fatalf("confirmed close result: mode=%v action=%q return=%v cmd=%v undo=%d", got.mode, got.confirmAction, got.confirmReturnMode, cmd != nil, len(got.activeUndoEntries()))
				}
			},
		},
		{
			name: "project history", id: commands.SearchProject,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeSessionSearch || !reflect.DeepEqual(got.filterProject, []string{"api"}) || cmd == nil {
					t.Fatalf("project history result: mode=%v filter=%v cmd=%v", got.mode, got.filterProject, cmd != nil)
				}
			},
		},
		{
			name: "details ignores banner", id: commands.ShowDetails,
			setup: func(m *Model) {
				m.deprecations = []config.Deprecation{{Key: "old", Replacement: "new"}}
				m.deprecationDismissed = false
			},
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeDetail || got.detailWindowID != 202 || got.detailViewTimeline || !got.detailLoading || got.deprecationDismissed || cmd == nil {
					t.Fatalf("details result: mode=%v window=%d timeline=%v loading=%v dismissed=%v cmd=%v", got.mode, got.detailWindowID, got.detailViewTimeline, got.detailLoading, got.deprecationDismissed, cmd != nil)
				}
			},
		},
		{
			name: "timeline", id: commands.ShowTimeline,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeDetail || got.detailWindowID != 202 || !got.detailViewTimeline || !got.detailLoading || cmd == nil {
					t.Fatalf("timeline result: mode=%v window=%d timeline=%v loading=%v cmd=%v", got.mode, got.detailWindowID, got.detailViewTimeline, got.detailLoading, cmd != nil)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, _ := commandTestModel(target)
			if tc.setup != nil {
				tc.setup(&m)
			}
			got, cmd := commandByID(t, tc.id).Run(m, ctx)
			if got.targetSession.TabID != 0 && got.targetSession.TabID != target.TabID {
				t.Fatalf("target tab = %d, want captured tab %d", got.targetSession.TabID, target.TabID)
			}
			tc.check(t, got, cmd)
		})
	}
}

func TestCommandExploreAndAppExecution(t *testing.T) {
	target := session.RunningSession{TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work"}

	t.Run("filter and clear filters", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commands.FilterSessions).Run(m, ctx)
		if got.mode != ModeSearch || got.filterText != "" || cmd != nil {
			t.Fatalf("filter result: mode=%v text=%q cmd=%v", got.mode, got.filterText, cmd != nil)
		}
		got.mode = ModeList
		got.activeFilter = dashboard.Parse("api")
		got.filtered = got.sessions
		got.rebuildItems()
		got, cmd = commandByID(t, commands.ClearFilters).Run(got, ctx)
		if !got.activeFilter.IsEmpty() || got.cursor != 0 || cmd != nil {
			t.Fatalf("clear result: filter=%+v cursor=%d cmd=%v", got.activeFilter, got.cursor, cmd != nil)
		}
	})

	t.Run("cycle profile filter", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commands.CycleProfileFilter).Run(m, ctx)
		if got.activeFilter.Account != "work" || got.activeFilter.Raw != "@work" || len(got.filtered) != 1 || cmd != nil {
			t.Fatalf("cycle result: filter=%+v rows=%d cmd=%v", got.activeFilter, len(got.filtered), cmd != nil)
		}
	})

	t.Run("launcher", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		t.Setenv(overrides.PathEnvVar, t.TempDir()+"/overrides.json")
		got, cmd := commandByID(t, commands.OpenLauncher).Run(m, ctx)
		if got.mode != ModePicker || len(got.pickerSessions) != 3 || len(got.pickerFilteredIdx) != 3 || got.pickerSearchMode || cmd != nil {
			t.Fatalf("launcher result: mode=%v sessions=%d filtered=%d search=%v cmd=%v", got.mode, len(got.pickerSessions), len(got.pickerFilteredIdx), got.pickerSearchMode, cmd != nil)
		}
	})

	t.Run("search surfaces", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commands.SearchAllSessions).Run(m, ctx)
		if got.mode != ModeSessionSearch || cmd == nil {
			t.Fatalf("session search result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
		m, ctx, _ = commandTestModel(target)
		got, cmd = commandByID(t, commands.SearchPlans).Run(m, ctx)
		if got.mode != ModePlanSearch || cmd == nil {
			t.Fatalf("plan search result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
	})

	// The inspector is on before the first press now, so the pair runs
	// hide-then-show rather than show-then-hide. What each half must prove is
	// unchanged: hiding releases the read target, and showing re-acquires it and
	// arms the read.
	t.Run("inspector", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		requireInspector(t, m)
		got, cmd := commandByID(t, commands.ToggleInspector).Run(m, ctx)
		if got.inspectorActive() || got.previewWindowID != 0 || cmd != nil {
			t.Fatalf("hide inspector result: active=%v window=%d cmd=%v", got.inspectorActive(), got.previewWindowID, cmd != nil)
		}
		got, cmd = commandByID(t, commands.ToggleInspector).Run(got, ctx)
		if !got.inspectorActive() || got.previewWindowID != 202 || cmd == nil {
			t.Fatalf("show inspector result: active=%v window=%d cmd=%v", got.inspectorActive(), got.previewWindowID, cmd != nil)
		}
		// Opening takes the keyboard: the press was a request to read output.
		if !got.inspectorFocused() {
			t.Fatalf("show inspector left the keyboard on the queue")
		}
	})

	t.Run("resort", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commands.ResortTabs).Run(m, ctx)
		if cmd == nil || len(got.toasts) != 1 || got.toasts[0].Text != "Resorting tabs…" {
			t.Fatalf("resort result: cmd=%v toasts=%v", cmd != nil, got.toasts)
		}
	})

	t.Run("undo", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		m.recovery = recoveryWith(undoTestNow, "api")
		m.now = func() time.Time { return undoTestNow }
		got, cmd := commandByID(t, commands.UndoClose).Run(m, ctx)
		// The entry is claimed rather than consumed: it comes off the offer
		// while the relaunch is in flight and is only gone once it succeeds.
		if cmd == nil || len(got.activeUndoEntries()) != 0 || len(got.toasts) != 1 || !strings.Contains(got.toasts[0].Text, "restoring") {
			t.Fatalf("undo result: cmd=%v available=%d toasts=%v", cmd != nil, len(got.activeUndoEntries()), got.toasts)
		}
	})

	t.Run("dismiss warning and help", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		m.deprecations = []config.Deprecation{{Key: "old", Replacement: "new"}}
		got, cmd := commandByID(t, commands.DismissDeprecation).Run(m, ctx)
		if !got.deprecationDismissed || got.mode != ModeList || cmd != nil {
			t.Fatalf("dismiss result: dismissed=%v mode=%v cmd=%v", got.deprecationDismissed, got.mode, cmd != nil)
		}
		got.helpOffset = 7
		got, cmd = commandByID(t, commands.ShowKeyboardHelp).Run(got, ctx)
		if got.mode != ModeHelp || got.helpOffset != 0 || cmd != nil {
			t.Fatalf("help result: mode=%v offset=%d cmd=%v", got.mode, got.helpOffset, cmd != nil)
		}
	})

	t.Run("quit", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commands.Quit).Run(m, ctx)
		if got.mode != ModeList || cmd == nil {
			t.Fatalf("quit result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("quit command returned %T, want tea.QuitMsg", cmd())
		}
	})
}

// TestPaletteRestartMatchesActionsOverlay is the inversion of the test that used
// to live here. It asserted that the two surfaces differed — the palette
// confirmed every restart while the overlay honored confirm_destructive — which
// was not a decision anybody made twice but one decision spelled twice and then
// edited once. Both surfaces now reach the shared controller, so with the
// preference off both dispatch immediately and with it on both stop at the same
// prompt.
func TestPaletteRestartMatchesActionsOverlay(t *testing.T) {
	target := session.RunningSession{TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work"}
	m, ctx, _ := commandTestModel(target)
	m.cfg.Defaults.ConfirmDestructive = false

	paletteResult, paletteCmd := commandByID(t, commands.RestartSession).Run(m, ctx)
	if paletteResult.mode != ModeList || paletteCmd == nil || len(paletteResult.toasts) != 1 {
		t.Fatalf("palette restart = mode %v cmd %v toasts %d; want the immediate toast path", paletteResult.mode, paletteCmd != nil, len(paletteResult.toasts))
	}

	m.targetSession = target
	m.mode = ModeActions
	m.actionsReturnMode = ModeList
	actionResult, actionCmd := rowActionRunners()[commands.RestartSession](m)
	if actionResult.mode != ModeList || actionCmd == nil || len(actionResult.toasts) != 1 {
		t.Fatalf("actions restart = mode %v cmd %v toasts %d; want the immediate toast path", actionResult.mode, actionCmd != nil, len(actionResult.toasts))
	}
}

func withCommandState(s session.RunningSession, state string) session.RunningSession {
	s.State = state
	return s
}

func withCommandLock(s session.RunningSession) session.RunningSession {
	s.Locked = true
	return s
}

func withCommandProject(s session.RunningSession, project string) session.RunningSession {
	s.Project = project
	s.Base = project
	return s
}
