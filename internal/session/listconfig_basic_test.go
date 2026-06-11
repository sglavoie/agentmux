package session_test

import (
	"encoding/json"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func newClientFromBytes(t *testing.T, data []byte) *kitty.SubprocessClient {
	t.Helper()
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": data,
	})
	return kitty.NewClientWithSocket(runner, "")
}

func newClientFromWindows(t *testing.T, windows []kitty.OSWindow) *kitty.SubprocessClient {
	t.Helper()
	data, err := json.Marshal(windows)
	if err != nil {
		t.Fatalf("marshal windows: %v", err)
	}
	return newClientFromBytes(t, data)
}

func TestListWithConfig_TitleFallback_HappyPath(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 20,
        "is_active": true,
        "title": "api",
        "windows": [
          {
            "id": 21,
            "is_active": true,
            "cwd": "/tmp",
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "api", Account: "work", Directory: "/projects/api"}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if len(ghosts) != 1 {
		t.Fatalf("expected 1 ghost, got %d", len(ghosts))
	}
	rs := sessions[0]
	if rs.Profile != "work" || rs.Project != "api" {
		t.Errorf("session: got (profile=%q project=%q), want (work, api)", rs.Profile, rs.Project)
	}
	if rs.Base != "api" {
		t.Errorf("Base: got %q, want %q", rs.Base, "api")
	}
	if rs.TabID != 20 || rs.WindowID != 21 {
		t.Errorf("ids: got tab=%d win=%d, want 20 / 21", rs.TabID, rs.WindowID)
	}
	g := ghosts[0]
	if g.Profile != "work" || g.Project != "api" || g.TabID != 20 || g.WindowID != 21 {
		t.Errorf("ghost: got %+v", g)
	}
	if g.Account.Name != "work" || g.Session.Title != "api" {
		t.Errorf("ghost cfg pair: got acc=%q sess=%q", g.Account.Name, g.Session.Title)
	}
}

func TestListWithConfig_TitleFallback_ProfileMatchCaseInsensitive(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 22,
        "is_active": true,
        "title": "api",
        "windows": [
          {
            "id": 23,
            "is_active": true,
            "cwd": "/tmp",
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "api", Profile: "Work", Directory: "/projects/api"}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Profile != "Work" || ghosts[0].Profile != "Work" {
		t.Fatalf("profile case was not preserved in fallback: session=%q ghost=%q", sessions[0].Profile, ghosts[0].Profile)
	}
}

func TestListWithConfig_TitleFallback_AmbiguousMatchSkipped(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 40,
        "is_active": true,
        "title": "api",
        "windows": [
          {
            "id": 41,
            "is_active": true,
            "cwd": "/tmp",
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "work", Harness: "claude"},
			{Name: "personal", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "api", Account: "work", Directory: "/projects/api"},
			{Title: "api", Account: "personal", Directory: "/projects/personal-api"},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions (ambiguous), got %d", len(sessions))
	}
	if len(ghosts) != 0 {
		t.Errorf("expected 0 ghosts (ambiguous), got %d", len(ghosts))
	}
}

func TestListWithConfig_TitleFallback_NoConfigMatch(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 50,
        "is_active": true,
        "title": "totally-not-configured",
        "windows": [
          {
            "id": 51,
            "is_active": true,
            "cwd": "/tmp",
            "user_vars": {}
          }
        ]
      }
    ]
  }
]`)
	client := newClientFromBytes(t, fixture)

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "api", Account: "work", Directory: "/projects/api"}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 0 || len(ghosts) != 0 {
		t.Errorf("expected no sessions/ghosts for lookalike title, got %d/%d", len(sessions), len(ghosts))
	}
}

func TestListWithConfig_CWDFallback_HappyPath(t *testing.T) {
	projectDir := t.TempDir()
	staleDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       60,
			IsActive: true,
			Title:    "codex",
			Windows: []kitty.Window{{
				ID:       61,
				IsActive: true,
				CWD:      staleDir,
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"},
					CWD:     projectDir,
				}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "claude"}},
		Sessions: []config.Session{{Title: "LogScanner", Account: "work", Directory: projectDir}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	rs := sessions[0]
	if rs.Project != "LogScanner" || rs.Base != "LogScanner" || rs.Profile != "work" {
		t.Fatalf("session identity = %+v, want work/LogScanner", rs)
	}
	if rs.Directory != projectDir {
		t.Fatalf("Directory = %q, want %q", rs.Directory, projectDir)
	}
	if rs.TabID != 60 || rs.WindowID != 61 {
		t.Fatalf("ids: got tab=%d win=%d, want 60 / 61", rs.TabID, rs.WindowID)
	}
	if got, want := rs.Key, session.SessionKey("work", "LogScanner"); got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
	g := ghosts[0]
	if g.Profile != "work" || g.Project != "LogScanner" || g.TabID != 60 || g.WindowID != 61 {
		t.Fatalf("ghost = %+v, want work/LogScanner at 60/61", g)
	}
}

func TestListWithConfig_ManagedWindowRepairsStaleUserVarsFromForegroundCWD(t *testing.T) {
	analyticsDir := t.TempDir()
	logScannerDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       62,
			IsActive: true,
			Title:    "codex",
			Windows: []kitty.Window{{
				ID:       63,
				IsActive: true,
				CWD:      analyticsDir,
				UserVars: kitty.UserVars{
					Profile:      "work",
					Harness:      "codex",
					Project:      "Analytics",
					AMXDirectory: analyticsDir,
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"node", "/opt/homebrew/bin/codex", "--yolo"},
					CWD:     logScannerDir,
				}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: "codex"}},
		Sessions: []config.Session{
			{Title: "Analytics", Account: "work", Directory: analyticsDir},
			{Title: "LogScanner", Account: "work", Directory: logScannerDir},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected repaired session + ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Project != "LogScanner" || sessions[0].Directory != logScannerDir || sessions[0].WindowID != 63 {
		t.Fatalf("session = %+v, want repaired LogScanner window 63", sessions[0])
	}
	if ghosts[0].Project != "LogScanner" || ghosts[0].WindowID != 63 {
		t.Fatalf("ghost = %+v, want LogScanner reanchor on window 63", ghosts[0])
	}
}

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
				{ID: 71, CWD: otherDir},
				{ID: 72, IsActive: true, CWD: projectDir},
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
			Windows:  []kitty.Window{{ID: 81, IsActive: true, CWD: projectDir}},
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
			Windows:  []kitty.Window{{ID: 91, IsActive: true, CWD: backendDir}},
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
