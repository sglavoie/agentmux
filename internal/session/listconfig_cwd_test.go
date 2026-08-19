package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

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

func TestListWithConfig_CWDFallbackMatchesLinkedGitWorktree(t *testing.T) {
	git := requireSessionGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "vta_ss_oidc_op_worker")
	linked := filepath.Join(root, "vta_ss_oidc_op_worker-security-audit")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("Mkdir repo: %v", err)
	}
	runSessionGit(t, git, repo, "init")
	runSessionGit(t, git, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runSessionGit(t, git, repo, "worktree", "add", "-b", "security/audit-extended", linked)

	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       66,
			IsActive: true,
			Title:    "vta_ss_oidc_op_worker-security-audit",
			Windows: []kitty.Window{{
				ID:       67,
				IsActive: true,
				CWD:      linked,
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"claude"},
					CWD:     linked,
				}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{{Name: "vta-claude", Harness: "claude"}},
		Sessions: []config.Session{{Title: "sis", Profile: "vta-claude", Directory: repo}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 || len(ghosts) != 1 {
		t.Fatalf("expected 1 session + 1 ghost, got %d/%d", len(sessions), len(ghosts))
	}
	rs := sessions[0]
	if rs.Project != "sis" || rs.Profile != "vta-claude" || rs.Directory != repo {
		t.Fatalf("session = %+v, want vta-claude/sis at %q", rs, repo)
	}
	if got, want := rs.Key, session.SessionKey("vta-claude", "sis"); got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
	if ghosts[0].Project != "sis" || ghosts[0].Profile != "vta-claude" || ghosts[0].WindowID != 67 {
		t.Fatalf("ghost = %+v, want vta-claude/sis on window 67", ghosts[0])
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

func TestListWithConfig_ManagedWindowPreservesRuntimeProfileOverride(t *testing.T) {
	projectDir := t.TempDir()
	client := newClientFromWindows(t, []kitty.OSWindow{{
		ID:       1,
		IsActive: true,
		Tabs: []kitty.Tab{{
			ID:       68,
			IsActive: true,
			Title:    "sis",
			Windows: []kitty.Window{{
				ID:       201,
				IsActive: true,
				CWD:      projectDir,
				UserVars: kitty.UserVars{
					Profile:      "tradition-claude",
					Account:      "tradition-claude",
					Harness:      "claude",
					Project:      "sis",
					AMXDirectory: projectDir,
				},
				ForegroundProcesses: []kitty.ForegroundProcess{{
					Cmdline: []string{"claude", "--dangerously-skip-permissions"},
					CWD:     projectDir,
				}},
			}},
		}},
	}})

	cfg := &config.Config{
		Accounts: []config.Account{
			{Name: "vta-claude", Harness: "claude"},
			{Name: "tradition-claude", Harness: "claude"},
		},
		Sessions: []config.Session{{
			Title: "sis", Profile: "vta-claude", Directory: projectDir,
		}},
	}

	sessions, ghosts, err := session.ListWithConfig(client, cfg)
	if err != nil {
		t.Fatalf("ListWithConfig: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if len(ghosts) != 0 {
		t.Fatalf("runtime profile override produced %d ghost repair(s), want 0", len(ghosts))
	}
	if got := sessions[0].EffectiveProfile(); got != "tradition-claude" {
		t.Fatalf("effective profile = %q, want runtime override tradition-claude", got)
	}
	if got, want := sessions[0].Key, session.SessionKey("tradition-claude", "sis"); got != want {
		t.Fatalf("session key = %q, want %q", got, want)
	}
}
