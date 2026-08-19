package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestListWithConfig_ManagedWindowRepairsStaleUserVarsFromHarnessEmojiTitle(t *testing.T) {
	analyticsDir := t.TempDir()
	logScannerDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       64,
			IsActive: true,
			Title:    "🛑📟🔍LogScanner",
			Windows: []kitty.Window{{
				ID:       65,
				IsActive: true,
				CWD:      analyticsDir,
				UserVars: kitty.UserVars{
					Profile:      "work",
					Harness:      "codex",
					Project:      "Analytics",
					AMXDirectory: analyticsDir,
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"codex"},
				}},
			}},
		}},
	}})

	cfg := &config.Config{
		HarnessEmojis: map[string]string{"codex": "📟"},
		Accounts:      []config.Account{{Name: "work", Harness: "codex"}},
		Sessions: []config.Session{
			{Title: "Analytics", Account: "work", Directory: analyticsDir},
			{Title: "🔍LogScanner", Account: "work", Directory: logScannerDir},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected repaired session + ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Project != "🔍LogScanner" || sessions[0].Base != "🔍LogScanner" {
		t.Fatalf("session = %+v, want title-repaired 🔍LogScanner", sessions[0])
	}
	if sessions[0].Directory != logScannerDir || sessions[0].WindowID != 65 {
		t.Fatalf("session directory/window = %q/%d, want %q/65", sessions[0].Directory, sessions[0].WindowID, logScannerDir)
	}
	if ghosts[0].Project != "🔍LogScanner" || ghosts[0].WindowID != 65 {
		t.Fatalf("ghost = %+v, want 🔍LogScanner reanchor on window 65", ghosts[0])
	}
}

func TestListWithConfig_CWDFallback_PrefersActiveWindow(t *testing.T) {
	otherDir := t.TempDir()
	projectDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       70,
			IsActive: true,
			Title:    "codex",
			Windows: []kitty.Window{
				{
					ID: 71, CWD: otherDir,
					ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"claude"}, CWD: otherDir}},
				},
				{
					ID: 72, IsActive: true, CWD: projectDir,
					ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"claude"}, CWD: projectDir}},
				},
			},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{
			{Title: "Other", Account: "work", Directory: otherDir},
			{Title: "LogScanner", Account: "work", Directory: projectDir},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Project != "LogScanner" || sessions[0].WindowID != 72 {
		t.Fatalf("session = %+v, want active LogScanner window 72", sessions[0])
	}
}

func TestListWithConfig_CWDFallback_AmbiguousDirectorySkipped(t *testing.T) {
	projectDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       80,
			IsActive: true,
			Title:    "codex",
			Windows: []kitty.Window{{
				ID: 81, IsActive: true, CWD: projectDir,
				ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"codex"}, CWD: projectDir}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{
			{Title: "LogScanner", Account: "work", Directory: projectDir},
			{Title: "LogScannerAlt", Account: "work", Directory: projectDir},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 0 || len(ghosts) != 0 {
		t.Fatalf("expected no sessions/ghosts for ambiguous cwd, got %d/%d", len(sessions), len(ghosts))
	}
}

func TestListWithConfig_TitleFallbackBeatsCWDFallback(t *testing.T) {
	apiDir := t.TempDir()
	backendDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       90,
			IsActive: true,
			Title:    "api",
			Windows: []kitty.Window{{
				ID: 91, IsActive: true, CWD: backendDir,
				ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"claude"}, CWD: backendDir}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{
			{Title: "api", Account: "work", Directory: apiDir},
			{Title: "backend", Account: "work", Directory: backendDir},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Project != "api" || sessions[0].Directory != apiDir {
		t.Fatalf("session = %+v, want title fallback api at %q", sessions[0], apiDir)
	}
}
