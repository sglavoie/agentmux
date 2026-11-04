package actions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

type continuityReader struct {
	harness string
	err     error
	reads   int
}

type continuityWorktree struct {
	requests  []worktree.Request
	created   worktree.Result
	createErr error
	removed   []worktree.Result
	removeErr error
}

func (w *continuityWorktree) Create(_ context.Context, request worktree.Request) (worktree.Result, error) {
	w.requests = append(w.requests, request)
	if w.createErr != nil {
		return worktree.Result{}, w.createErr
	}
	return w.created, nil
}

func (w *continuityWorktree) RemoveUnused(_ context.Context, result worktree.Result) error {
	w.removed = append(w.removed, result)
	return w.removeErr
}

func (r *continuityReader) Harness() string { return r.harness }
func (*continuityReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	return nil, nil
}
func (r *continuityReader) ReadMessages(string) ([]transcript.Message, error) {
	r.reads++
	if r.err != nil {
		return nil, r.err
	}
	return nil, nil
}

func continuityConfig(harnessName string) *config.Config {
	return &config.Config{
		Defaults: config.Defaults{Args: []string{"--model", "test"}},
		Accounts: []config.Account{{Name: "work", Harness: harnessName, HomeDir: "/tmp/agentmux-profile"}},
		Projects: []config.Session{{Title: "api", Directory: "/tmp/agentmux-project", Profile: "work", Args: []string{"--project-flag"}}},
	}
}

func continuityMeta(t *testing.T, harnessName, id, profile string) transcript.SessionMeta {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return transcript.SessionMeta{ID: id, Harness: harnessName, Profile: profile, Project: "api", CWD: "/tmp/agentmux-project", Path: path}
}

// A harness records the physical directory it ran in. A project configured
// through a symlink to that directory is still the project the conversation
// ran in, so resume and fork carry its args forward.
func TestContinuityProjectMatchesAProjectConfiguredThroughASymlink(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	real := filepath.Join(base, "real", "api")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "real"), link); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Projects: []config.Session{{Title: "API", Directory: filepath.Join(link, "api"), Profile: "work", Args: []string{"--project-arg"}}}}
	account := config.Account{Name: "work", Harness: harness.Claude}

	project := continuityProject(cfg, transcript.SessionMeta{ID: "s-1", Project: "API", CWD: physical}, account)
	if project.Title != "API" || len(project.Args) != 1 || project.Args[0] != "--project-arg" || project.Directory != physical {
		t.Fatalf("project = %+v, want the symlinked project with its args in the recorded directory", project)
	}
}
