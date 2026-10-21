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
	t.Parallel()

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
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/tmp"}
            ],
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
	t.Parallel()

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
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/projects/api"}
            ],
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
	t.Parallel()

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
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/tmp"}
            ],
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
	t.Parallel()

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
            "foreground_processes": [
              {"cmdline": ["claude"], "cwd": "/tmp"}
            ],
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
