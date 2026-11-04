package handoff

import (
	"context"
	"errors"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type workflowReader struct {
	harness  string
	messages []transcript.Message
}

func (r workflowReader) Harness() string { return r.harness }
func (workflowReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	return nil, nil
}
func (r workflowReader) ReadMessages(string) ([]transcript.Message, error) {
	return append([]transcript.Message(nil), r.messages...), nil
}

type workflowGit struct{}

func (workflowGit) Inspect(context.Context, string, time.Duration) GitState {
	return GitState{Kind: "not_git", Detail: "test workspace is not a Git repository"}
}

func testWorkflow(t *testing.T, launch func(session.Runtime, config.Session, config.Account, *config.Config, string) error, edit func(string, *config.Config) error) *Workflow {
	t.Helper()
	artifactDirectory := filepath.Join(t.TempDir(), "handoffs")
	if launch == nil {
		launch = func(session.Runtime, config.Session, config.Account, *config.Config, string) error { return nil }
	}
	if edit == nil {
		edit = func(string, *config.Config) error { return nil }
	}
	return &Workflow{
		builder:           NewBuilderWithGit(workflowGit{}),
		artifactDirectory: func() (string, error) { return artifactDirectory, nil },
		edit:              edit,
		launch: func(rt session.Runtime, sess config.Session, acc config.Account, cfg *config.Config, prompt string) (session.LaunchResult, error) {
			return session.LaunchResult{}, launch(rt, sess, acc, cfg, prompt)
		},
		now:   func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) },
		newID: func() (string, error) { return "handoff-test", nil },
	}
}

func TestWorkflowLaunchesEverySourceTargetHarnessCombination(t *testing.T) {
	targets := []string{harness.Claude, harness.Codex, harness.Copilot}
	for _, sourceHarness := range targets {
		for _, targetHarness := range targets {
			t.Run(sourceHarness+"_to_"+targetHarness, func(t *testing.T) {
				cwd := t.TempDir()
				sourcePath := filepath.Join(cwd, "source.jsonl")
				if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				targetProfile := "target-" + targetHarness
				cfg := &config.Config{
					Accounts: []config.Account{{Name: "source", Harness: sourceHarness}, {Name: targetProfile, Harness: targetHarness, HomeDir: "/tmp/target-home"}},
					Projects: []config.Session{{Title: "api", Directory: cwd, Profile: targetProfile, Args: []string{"--target-arg"}}},
				}
				var gotSession config.Session
				var gotAccount config.Account
				var gotPrompt string
				workflow := testWorkflow(t, func(_ session.Runtime, sess config.Session, acc config.Account, _ *config.Config, prompt string) error {
					gotSession, gotAccount, gotPrompt = sess, acc, prompt
					return nil
				}, nil)
				result, err := workflow.Run(context.Background(), Request{
					Source: transcript.SessionMeta{ID: "source-1", Harness: sourceHarness, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
					Reader: workflowReader{harness: sourceHarness, messages: []transcript.Message{{Role: "user", Text: "Continue the migration."}}},
					Config: cfg, TargetProfile: targetProfile,
					Runtime: session.Runtime{Tmux: selectedTmux{}},
				})
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				if !result.Launched || result.Artifact.Status != StatusFinal {
					t.Fatalf("result = %+v, want a finalized launched handoff", result)
				}
				if gotSession.Directory != cwd || gotSession.EffectiveProfile() != targetProfile || gotAccount.EffectiveHarness() != targetHarness {
					t.Fatalf("launch context = session=%+v account=%+v, want source cwd and target profile/harness", gotSession, gotAccount)
				}
				if !strings.Contains(gotPrompt, result.Artifact.Path) || !strings.Contains(gotPrompt, "Verify all assumptions") {
					t.Fatalf("prompt = %q, want handoff path and stale-assumption instruction", gotPrompt)
				}
				if info, err := os.Stat(result.Artifact.Path); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("final artifact = %v, %v; want private regular file", info, err)
				}
				content, err := os.ReadFile(result.Artifact.Path)
				if err != nil || !strings.Contains(string(content), "Project: `api`") {
					t.Fatalf("artifact project label: %v\n%s", err, content)
				}
			})
		}
	}
}

func TestWorkflowEditFinalizesEditedDraftBeforeLaunch(t *testing.T) {
	cwd := t.TempDir()
	sourcePath := filepath.Join(cwd, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workflow := testWorkflow(t, nil, func(path string, _ *config.Config) error {
		return os.WriteFile(path, []byte("# Edited Handoff\n\nUse current tests.\n"), 0o600)
	})
	result, err := workflow.Run(context.Background(), Request{
		Source:        transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
		Reader:        workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Original goal"}}},
		Config:        &config.Config{Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		TargetProfile: "target", Edit: true, NoLaunch: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.DraftPath == "" || result.Artifact.Path == "" || result.Launched {
		t.Fatalf("result = %+v, want retained draft and finalized create-only artifact", result)
	}
	final, err := os.ReadFile(result.Artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(final) != "# Edited Handoff\n\nUse current tests.\n" {
		t.Fatalf("final content = %q, want edited draft content", final)
	}
	requireMode(t, result.DraftPath, 0o600)
	requireMode(t, result.Artifact.Path, 0o600)
}

func TestWorkflowRetainsFinalArtifactWhenLaunchFails(t *testing.T) {
	cwd := t.TempDir()
	sourcePath := filepath.Join(cwd, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workflow := testWorkflow(t, func(session.Runtime, config.Session, config.Account, *config.Config, string) error {
		return errors.New("tmux launch failed")
	}, nil)
	result, err := workflow.Run(context.Background(), Request{
		Source:        transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
		Reader:        workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Original goal"}}},
		Config:        &config.Config{Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		TargetProfile: "target", Runtime: session.Runtime{Tmux: selectedTmux{}},
	})
	var partial *PartialLaunchError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %v, want PartialLaunchError", err)
	}
	if result.Artifact.Status != StatusFinal || result.Artifact.Path == "" {
		t.Fatalf("partial result = %+v, want finalized artifact", result)
	}
	if _, statErr := os.Stat(result.Artifact.Path); statErr != nil {
		t.Fatalf("final artifact was not retained: %v", statErr)
	}
}

// TestWorkflowLaunchesOnSelectedRuntime pins that the handoff target launches
// on the backend the request selects, and that a failed launch there still
// retains the finalized artifact instead of retrying or launching elsewhere.
func TestWorkflowLaunchesOnSelectedRuntime(t *testing.T) {
	cwd := t.TempDir()
	sourcePath := filepath.Join(cwd, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := session.Runtime{Tmux: selectedTmux{}}
	workflow := testWorkflow(t, nil, nil)
	var launches []session.Runtime
	workflow.launch = func(rt session.Runtime, _ config.Session, _ config.Account, _ *config.Config, _ string) (session.LaunchResult, error) {
		launches = append(launches, rt)
		return session.LaunchResult{}, errors.New("tmux launch failed")
	}
	result, err := workflow.Run(context.Background(), Request{
		Source:        transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
		Reader:        workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Original goal"}}},
		Config:        &config.Config{Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		TargetProfile: "target", Runtime: selected,
	})
	var partial *PartialLaunchError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %v, want PartialLaunchError", err)
	}
	if len(launches) != 1 || launches[0].Kind() != runtimesession.BackendTmux {
		t.Fatalf("launches = %+v, want exactly one tmux launch", launches)
	}
	if _, statErr := os.Stat(result.Artifact.Path); statErr != nil {
		t.Fatalf("final artifact was not retained: %v", statErr)
	}
}

// selectedTmux only marks a runtime as tmux; the injected launch never
// reaches it.
type selectedTmux struct{ backend.TargetLauncher }

func TestProjectLabelPrefersTranscriptThenRepositoryThenSession(t *testing.T) {
	tests := []struct {
		name   string
		source transcript.SessionMeta
		want   string
	}{
		{name: "transcript metadata", source: transcript.SessionMeta{Project: " API ", CWD: "/src/repository", ID: "session-1"}, want: "API"},
		{name: "repository basename", source: transcript.SessionMeta{CWD: "/src/repository", ID: "session-1"}, want: "repository"},
		{name: "session ID", source: transcript.SessionMeta{CWD: string(filepath.Separator), ID: "session-1"}, want: "session-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := projectLabel(test.source); got != test.want {
				t.Fatalf("projectLabel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWorkflowUsesExplicitLaunchDirectoryWithoutChangingSourcePackContext(t *testing.T) {
	sourceCWD := t.TempDir()
	launchCWD := t.TempDir()
	sourcePath := filepath.Join(sourceCWD, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var launched config.Session
	workflow := testWorkflow(t, func(_ session.Runtime, session config.Session, _ config.Account, _ *config.Config, _ string) error {
		launched = session
		return nil
	}, nil)
	_, err := workflow.Run(context.Background(), Request{
		Source:        transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: sourceCWD, Path: sourcePath},
		Reader:        workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Original goal"}}},
		Config:        &config.Config{Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		TargetProfile: "target", LaunchDirectory: launchCWD, Runtime: session.Runtime{Tmux: selectedTmux{}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if launched.Directory != launchCWD {
		t.Fatalf("launch directory = %q, want %q", launched.Directory, launchCWD)
	}
}

// Two configured projects share the target profile and the source directory,
// and neither title names the source. Neither is the project the source ran
// in, so the target launches from a bare template rather than with the args
// of whichever came first.
func TestWorkflowDoesNotGuessAmongSeveralMatchingProjects(t *testing.T) {
	cwd := t.TempDir()
	sourcePath := filepath.Join(cwd, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex, HomeDir: "/tmp/target-home"}},
		Projects: []config.Session{
			{Title: "api-fast", Directory: cwd, Profile: "target", Args: []string{"--fast"}},
			{Title: "api-safe", Directory: cwd, Profile: "target", Args: []string{"--safe"}},
		},
	}
	var got config.Session
	workflow := testWorkflow(t, func(_ session.Runtime, sess config.Session, _ config.Account, _ *config.Config, _ string) error {
		got = sess
		return nil
	}, nil)
	if _, err := workflow.Run(context.Background(), Request{
		Source: transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
		Reader: workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Continue."}}},
		Config: cfg, TargetProfile: "target",
		Runtime: session.Runtime{Tmux: selectedTmux{}},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.Args) != 0 || len(got.ClaudeFlags) != 0 || got.Title != "api" || got.Directory != cwd || got.EffectiveProfile() != "target" {
		t.Fatalf("launch session = %+v, want a bare template titled api in the source directory", got)
	}

	// With only one of them configured, it is the project and its args come along.
	cfg.Projects = cfg.Projects[:1]
	if _, err := workflow.Run(context.Background(), Request{
		Source: transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
		Reader: workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Continue."}}},
		Config: cfg, TargetProfile: "target",
		Runtime: session.Runtime{Tmux: selectedTmux{}},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.Args) != 1 || got.Args[0] != "--fast" || got.Title != "api" {
		t.Fatalf("launch session = %+v, want the one matching project's args under the source title", got)
	}
}

// A harness records the physical directory it ran in; a target project
// configured through a symlink to it is still that directory's project.
func TestWorkflowMatchesATargetProjectConfiguredThroughASymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(cwd, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex, HomeDir: "/tmp/target-home"}},
		Projects: []config.Session{{Title: "api", Directory: link, Profile: "target", Args: []string{"--target-arg"}}},
	}
	var got config.Session
	workflow := testWorkflow(t, func(_ session.Runtime, sess config.Session, _ config.Account, _ *config.Config, _ string) error {
		got = sess
		return nil
	}, nil)
	if _, err := workflow.Run(context.Background(), Request{
		Source: transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
		Reader: workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Continue."}}},
		Config: cfg, TargetProfile: "target",
		Runtime: session.Runtime{Tmux: selectedTmux{}},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.Args) != 1 || got.Args[0] != "--target-arg" || got.Directory != cwd {
		t.Fatalf("launch session = %+v, want the symlinked project's args in the source directory", got)
	}
}

func TestPreviewIsReadOnlyAndReviewedLaunchPreservesEdits(t *testing.T) {
	cfg := &config.Config{Accounts: []config.Account{{Name: "target", Harness: harness.Codex}}}
	request := Request{Source: transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, CWD: t.TempDir(), Path: "source.jsonl"},
		Reader: workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Original goal"}}}, Config: cfg, Preview: true}
	workflow := testWorkflow(t, nil, nil)
	originalDirectory := workflow.artifactDirectory
	workflow.artifactDirectory = func() (string, error) { t.Fatal("preview attempted to create an artifact"); return "", nil }
	preview, err := workflow.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Content, "Original goal") || preview.SourceHarness != harness.Claude || preview.SourceSessionID != "source-1" || preview.Directory != request.Source.CWD || preview.Launched || preview.Artifact.Path != "" {
		t.Fatalf("preview = %+v", preview)
	}
	workflow.artifactDirectory = originalDirectory
	var prompt string
	workflow.launch = func(_ session.Runtime, target config.Session, account config.Account, _ *config.Config, initial string) (session.LaunchResult, error) {
		if target.Directory != request.Source.CWD || account.Name != "target" {
			t.Fatalf("target = %+v, %+v", target, account)
		}
		prompt = initial
		return session.LaunchResult{RuntimeSessionID: runtimesession.ID("rs-receiving")}, nil
	}
	request.Preview = false
	request.TargetProfile = "target"
	request.Runtime = session.Runtime{Tmux: selectedTmux{}}
	request.Content = []byte("Edited context with `quotes`, $() and a different goal.\n")
	request.Instruction = "Review for concurrency bugs."
	result, err := workflow.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(result.Artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := string(request.Content) + "\n\n## Next instruction\n\n" + request.Instruction + "\n"
	if string(content) != want || strings.Contains(string(content), "Original goal") {
		t.Fatalf("artifact = %q", content)
	}
	if !result.Launched || result.RuntimeSessionID != "rs-receiving" || !strings.Contains(prompt, result.Artifact.Path) {
		t.Fatalf("launch = %+v, %q", result, prompt)
	}
}

func TestReviewedContextValidationBeforeWriting(t *testing.T) {
	for _, content := range [][]byte{[]byte(" \n"), []byte(strings.Repeat("a", MaxReviewedBytes+1))} {
		workflow := testWorkflow(t, nil, nil)
		workflow.artifactDirectory = func() (string, error) { t.Fatal("invalid context wrote an artifact"); return "", nil }
		_, err := workflow.Run(context.Background(), Request{Source: transcript.SessionMeta{ID: "one", Harness: harness.Claude, CWD: t.TempDir(), Path: "source.jsonl"}, Reader: workflowReader{harness: harness.Claude}, Config: &config.Config{Accounts: []config.Account{{Name: "target"}}}, TargetProfile: "target", Content: content, NoLaunch: true})
		if err == nil {
			t.Fatal("invalid context accepted")
		}
	}
}
