package session_test

import (
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

func TestListWithConfig_TitleFallback_StripsStateIcon(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 30,
        "is_active": true,
        "title": "⚡api",
        "windows": [
          {
            "id": 31,
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
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	rs := sessions[0]
	if rs.State != "bolt" {
		t.Errorf("State: got %q, want %q", rs.State, "bolt")
	}
	if rs.Base != "api" {
		t.Errorf("Base: got %q, want %q", rs.Base, "api")
	}
	if rs.Project != "api" {
		t.Errorf("Project: got %q, want %q", rs.Project, "api")
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

func TestListWithConfig_TitleFallback_LegacyPrefixedTitle_UniqueMatch(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 45,
        "is_active": true,
        "title": "W:api",
        "windows": [
          {
            "id": 46,
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
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Profile != "work" || sessions[0].Project != "api" {
		t.Errorf("session: got %+v, want work/api", sessions[0])
	}
	if ghosts[0].Profile != "work" || ghosts[0].Project != "api" {
		t.Errorf("ghost: got %+v, want work/api", ghosts[0])
	}
}

func TestListWithConfig_TitleFallback_ExactTitleBeatsLegacyPrefix(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 47,
        "is_active": true,
        "title": "W:api",
        "windows": [
          {
            "id": 48,
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
			{Title: "W:api", Account: "personal", Directory: "/projects/personal-w-api"},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	if sessions[0].Profile != "personal" || sessions[0].Project != "W:api" {
		t.Errorf("session: got %+v, want personal/W:api", sessions[0])
	}
	if ghosts[0].Profile != "personal" || ghosts[0].Project != "W:api" {
		t.Errorf("ghost: got %+v, want personal/W:api", ghosts[0])
	}
}

func TestListWithConfig_TitleFallback_LegacyPrefixedTitle_AmbiguousMatchSkipped(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 49,
        "is_active": true,
        "title": "W:api",
        "windows": [
          {
            "id": 50,
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
			{Name: "warehouse", Harness: "claude"},
		},
		Sessions: []config.Session{
			{Title: "api", Account: "work", Directory: "/projects/api"},
			{Title: "api", Account: "warehouse", Directory: "/projects/wh-api"},
		},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions (ambiguous legacy prefix), got %d", len(sessions))
	}
	if len(ghosts) != 0 {
		t.Errorf("expected 0 ghosts (ambiguous legacy prefix), got %d", len(ghosts))
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

func TestListWithConfig_AnchoredWindow_NoGhostEmitted(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 60,
        "is_active": true,
        "title": "api",
        "windows": [
          {
            "id": 61,
            "is_active": true,
            "cwd": "/projects/api",
            "user_vars": {
              "cc_account": "work",
              "cc_cwd": "/projects/api"
            }
          },
          {
            "id": 62,
            "is_active": false,
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
		t.Fatalf("expected 1 session via normal detection, got %d", len(sessions))
	}
	if len(ghosts) != 0 {
		t.Errorf("expected 0 ghosts when anchor window is alive, got %d", len(ghosts))
	}
	if sessions[0].WindowID != 61 {
		t.Errorf("WindowID: got %d, want 61 (anchor)", sessions[0].WindowID)
	}
}

func TestList_PrefersActiveManagedWindowMetadata(t *testing.T) {
	fixture := []byte(`[
  {
    "id": 1,
    "is_active": true,
    "tabs": [
      {
        "id": 10,
        "is_active": true,
        "title": "split",
        "windows": [
          {
            "id": 11,
            "is_active": false,
            "cwd": "/tmp/stale",
            "user_vars": {
              "cc_account": "work",
              "cc_cwd": "/tmp/stale",
              "cc_flags": "--stale",
              "cc_groups": "stale"
            }
          },
          {
            "id": 12,
            "is_active": true,
            "cwd": "/tmp/active",
            "user_vars": {
              "cc_account": "work",
              "cc_cwd": "/tmp/active",
              "cc_flags": "--active",
              "cc_groups": "live"
            }
          }
        ]
      }
    ]
  }
]`)
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")

	sessions, err := session.List(client)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	got := sessions[0]
	if got.WindowID != 12 {
		t.Fatalf("WindowID: got %d, want 12", got.WindowID)
	}
	if got.Directory != "/tmp/active" {
		t.Errorf("Directory: got %q, want %q", got.Directory, "/tmp/active")
	}
	if got.Args != "--active" {
		t.Errorf("Args: got %q, want %q", got.Args, "--active")
	}
	if len(got.Groups) != 1 || got.Groups[0] != "live" {
		t.Errorf("Groups: got %v, want [live]", got.Groups)
	}
}
