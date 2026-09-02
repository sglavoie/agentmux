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
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

func TestCommandRunRejectsDisabledAndHiddenCommands(t *testing.T) {
	m, _, _ := commandTestModel(session.RunningSession{
		TabID: 1, WindowID: 101, Base: "api", Project: "api", Profile: "work", Locked: true,
	})
	ctx := captureCommandContext(m)
	before := m
	after, cmd := commandByID(t, commandRestartSession).run(m, ctx)
	if cmd != nil || after.mode != before.mode || after.confirmAction != "" {
		t.Fatalf("disabled restart mutated model: mode=%v action=%q cmd=%v", after.mode, after.confirmAction, cmd != nil)
	}
	after, cmd = commandByID(t, commandClearFilters).run(m, ctx)
	if cmd != nil || !after.activeFilter.IsEmpty() {
		t.Fatal("hidden clear-filters command should be a no-op")
	}
}

func TestCommandExecutionUsesCapturedTarget(t *testing.T) {
	client := NewUITestKittyClient(nil)
	m := testModelWithSessions()
	m.client = client
	m = loadSessions(m, []session.RunningSession{
		{TabID: 11, WindowID: 101, Base: "api", Project: "api", Profile: "work"},
		{TabID: 22, WindowID: 202, Base: "backend", Project: "backend", Profile: "work"},
	})
	m.cursor = 1
	ctx := captureCommandContext(m)
	m.cursor = 0 // A refresh/controller move after capture must not retarget.
	_, cmd := commandByID(t, commandFocusSession).run(m, ctx)
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
		id        commandID
		wantOp    string
		wantMatch string
		wantVar   string
	}{
		{"focus", commandFocusSession, "FocusTab", kitty.MatchTabByID(22), ""},
		{"peek", commandPeekScrollback, "LaunchOverlayPager", kitty.MatchWindowByID(202), ""},
		{"lock", commandToggleLock, "SetUserVars", kitty.MatchWindowByID(202), "amx_locked"},
		{"mute", commandToggleMute, "SetUserVars", kitty.MatchWindowByID(202), "cc_mute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, client := commandTestModel(target)
			_, cmd := commandByID(t, tc.id).run(m, ctx)
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

	originalCopy := copyToClipboard
	defer func() { copyToClipboard = originalCopy }()
	var copied string
	copyToClipboard = func(text string) error {
		copied = text
		return nil
	}
	_, copyCmd := commandByID(t, commandCopyTitle).run(m, ctx)
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
	_, clearCmd := commandByID(t, commandClearStatus).run(m, ctx)
	if clearCmd == nil {
		t.Fatal("clear status returned nil command")
	}
	_ = clearCmd()
	if request.Action != "user_clear" || request.WindowID != 202 {
		t.Fatalf("clear-status request = %+v, want user_clear for window 202", request)
	}
}

func TestCommandConfiguredProjectExecutions(t *testing.T) {
	target := session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work",
	}
	for _, id := range []commandID{commandOpenDirectory, commandOpenEditor} {
		t.Run(string(id), func(t *testing.T) {
			m, ctx, _ := commandTestModel(target)
			got, cmd := commandByID(t, id).run(m, ctx)
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
		id    commandID
		setup func(*Model)
		check func(*testing.T, Model, tea.Cmd)
	}
	cases := []transitionCase{
		{
			name: "restart always confirms", id: commandRestartSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = false },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfirm || got.confirmAction != "restart" || got.confirmConsequence != restartConsequence || got.confirmReturnMode != ModeList || cmd != nil {
					t.Fatalf("restart result: mode=%v action=%q consequence=%q return=%v cmd=%v", got.mode, got.confirmAction, got.confirmConsequence, got.confirmReturnMode, cmd != nil)
				}
			},
		},
		{
			name: "override profile picker", id: commandOverrideProfile,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfigPicker || got.configPickerPurpose != launch.ConfigPickerRestartProfile || got.configPickerReturnMode != ModeList || got.configPickerCurrent != "work" || got.configPickerProjectIdx != -1 || cmd != nil {
					t.Fatalf("override result: mode=%v purpose=%v return=%v current=%q project=%d cmd=%v", got.mode, got.configPickerPurpose, got.configPickerReturnMode, got.configPickerCurrent, got.configPickerProjectIdx, cmd != nil)
				}
			},
		},
		{
			name: "launch another picker", id: commandLaunchAnother,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfigPicker || got.configPickerPurpose != launch.ConfigPickerDashboardLaunch || got.configPickerReturnMode != ModeList || got.configPickerCurrent != "work" || cmd != nil {
					t.Fatalf("launch result: mode=%v purpose=%v return=%v current=%q cmd=%v", got.mode, got.configPickerPurpose, got.configPickerReturnMode, got.configPickerCurrent, cmd != nil)
				}
			},
		},
		{
			name: "close without confirmation", id: commandCloseSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = false },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeList || cmd == nil || len(got.pendingUndo) != 1 || len(got.toasts) != 1 || !strings.Contains(got.toasts[0].Text, "undo") {
					t.Fatalf("close result: mode=%v cmd=%v undo=%d toasts=%v", got.mode, cmd != nil, len(got.pendingUndo), got.toasts)
				}
			},
		},
		{
			name: "close with confirmation", id: commandCloseSession,
			setup: func(m *Model) { m.cfg.Defaults.ConfirmDestructive = true },
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeConfirm || got.confirmAction != "close" || got.confirmConsequence != closeConsequence || got.confirmReturnMode != ModeList || cmd != nil || len(got.pendingUndo) != 0 {
					t.Fatalf("confirmed close result: mode=%v action=%q return=%v cmd=%v undo=%d", got.mode, got.confirmAction, got.confirmReturnMode, cmd != nil, len(got.pendingUndo))
				}
			},
		},
		{
			name: "project history", id: commandSearchProject,
			check: func(t *testing.T, got Model, cmd tea.Cmd) {
				if got.mode != ModeSessionSearch || !reflect.DeepEqual(got.filterProject, []string{"api"}) || cmd == nil {
					t.Fatalf("project history result: mode=%v filter=%v cmd=%v", got.mode, got.filterProject, cmd != nil)
				}
			},
		},
		{
			name: "details ignores banner", id: commandShowDetails,
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
			name: "timeline", id: commandShowTimeline,
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
			got, cmd := commandByID(t, tc.id).run(m, ctx)
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
		got, cmd := commandByID(t, commandFilterSessions).run(m, ctx)
		if got.mode != ModeSearch || got.filterText != "" || cmd != nil {
			t.Fatalf("filter result: mode=%v text=%q cmd=%v", got.mode, got.filterText, cmd != nil)
		}
		got.mode = ModeList
		got.activeFilter = dashboard.Parse("api")
		got.filtered = got.sessions
		got, cmd = commandByID(t, commandClearFilters).run(got, ctx)
		if !got.activeFilter.IsEmpty() || got.cursor != 0 || cmd != nil {
			t.Fatalf("clear result: filter=%+v cursor=%d cmd=%v", got.activeFilter, got.cursor, cmd != nil)
		}
	})

	t.Run("cycle profile filter", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandCycleProfileFilter).run(m, ctx)
		if got.activeFilter.Account != "work" || got.activeFilter.Raw != "@work" || len(got.filtered) != 1 || cmd != nil {
			t.Fatalf("cycle result: filter=%+v rows=%d cmd=%v", got.activeFilter, len(got.filtered), cmd != nil)
		}
	})

	t.Run("launcher", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		t.Setenv(overrides.PathEnvVar, t.TempDir()+"/overrides.json")
		got, cmd := commandByID(t, commandOpenLauncher).run(m, ctx)
		if got.mode != ModePicker || len(got.pickerSessions) != 3 || len(got.pickerFilteredIdx) != 3 || got.pickerSearchMode || cmd != nil {
			t.Fatalf("launcher result: mode=%v sessions=%d filtered=%d search=%v cmd=%v", got.mode, len(got.pickerSessions), len(got.pickerFilteredIdx), got.pickerSearchMode, cmd != nil)
		}
	})

	t.Run("search surfaces", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandSearchAllSessions).run(m, ctx)
		if got.mode != ModeSessionSearch || cmd == nil {
			t.Fatalf("session search result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
		m, ctx, _ = commandTestModel(target)
		got, cmd = commandByID(t, commandSearchPlans).run(m, ctx)
		if got.mode != ModePlanSearch || cmd == nil {
			t.Fatalf("plan search result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
	})

	t.Run("preview", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandTogglePreview).run(m, ctx)
		if !got.previewEnabled || got.previewWindowID != 202 || cmd == nil {
			t.Fatalf("show preview result: enabled=%v window=%d cmd=%v", got.previewEnabled, got.previewWindowID, cmd != nil)
		}
		got, cmd = commandByID(t, commandTogglePreview).run(got, ctx)
		if got.previewEnabled || got.previewWindowID != 0 || cmd != nil {
			t.Fatalf("hide preview result: enabled=%v window=%d cmd=%v", got.previewEnabled, got.previewWindowID, cmd != nil)
		}
	})

	t.Run("resort", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandResortTabs).run(m, ctx)
		if cmd == nil || len(got.toasts) != 1 || got.toasts[0].Text != "Resorting tabs…" {
			t.Fatalf("resort result: cmd=%v toasts=%v", cmd != nil, got.toasts)
		}
	})

	t.Run("undo", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		m.pendingUndo = []undoEntry{{
			snapshot: config.Session{Title: "api", Directory: "/home/user/api", Profile: "work"},
			account:  config.Account{Name: "work", Color: "blue"},
			deadline: time.Now().Add(time.Minute), label: "api",
		}}
		got, cmd := commandByID(t, commandUndoClose).run(m, ctx)
		if cmd == nil || len(got.pendingUndo) != 0 || len(got.toasts) != 1 || !strings.Contains(got.toasts[0].Text, "restored") {
			t.Fatalf("undo result: cmd=%v pending=%d toasts=%v", cmd != nil, len(got.pendingUndo), got.toasts)
		}
	})

	t.Run("dismiss warning and help", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		m.deprecations = []config.Deprecation{{Key: "old", Replacement: "new"}}
		got, cmd := commandByID(t, commandDismissDeprecation).run(m, ctx)
		if !got.deprecationDismissed || got.mode != ModeList || cmd != nil {
			t.Fatalf("dismiss result: dismissed=%v mode=%v cmd=%v", got.deprecationDismissed, got.mode, cmd != nil)
		}
		got.helpOffset = 7
		got, cmd = commandByID(t, commandShowKeyboardHelp).run(got, ctx)
		if got.mode != ModeHelp || got.helpOffset != 0 || cmd != nil {
			t.Fatalf("help result: mode=%v offset=%d cmd=%v", got.mode, got.helpOffset, cmd != nil)
		}
	})

	t.Run("quit", func(t *testing.T) {
		m, ctx, _ := commandTestModel(target)
		got, cmd := commandByID(t, commandQuit).run(m, ctx)
		if got.mode != ModeList || cmd == nil {
			t.Fatalf("quit result: mode=%v cmd=%v", got.mode, cmd != nil)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("quit command returned %T, want tea.QuitMsg", cmd())
		}
	})
}

func TestPaletteRestartDiffersFromActionsOverlay(t *testing.T) {
	target := session.RunningSession{TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work"}
	m, ctx, _ := commandTestModel(target)
	m.cfg.Defaults.ConfirmDestructive = false

	paletteResult, paletteCmd := commandByID(t, commandRestartSession).run(m, ctx)
	if paletteResult.mode != ModeConfirm || paletteCmd != nil {
		t.Fatalf("palette restart = mode %v cmd %v; want confirmation", paletteResult.mode, paletteCmd != nil)
	}

	m.targetSession = target
	m.mode = ModeActions
	m.actionsReturnMode = ModeList
	action := actionsFor(m)[2]
	actionResult, actionCmd := action.run(m)
	if actionResult.mode != ModeList || actionCmd == nil || len(actionResult.toasts) != 1 {
		t.Fatalf("actions restart = mode %v cmd %v toasts %d; want immediate toast path", actionResult.mode, actionCmd != nil, len(actionResult.toasts))
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
