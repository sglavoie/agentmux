package handoff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
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

func testWorkflow(t *testing.T, launch func(kitty.KittyClient, config.Session, config.Account, *config.Config, string) error, edit func(string, *config.Config) error) *Workflow {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "agent-mux.db")
	artifactDirectory := filepath.Join(t.TempDir(), "handoffs")
	if launch == nil {
		launch = func(kitty.KittyClient, config.Session, config.Account, *config.Config, string) error { return nil }
	}
	if edit == nil {
		edit = func(string, *config.Config) error { return nil }
	}
	return &Workflow{
		builder: NewBuilderWithGit(workflowGit{}),
		openStore: func(ctx context.Context) (*datastore.Store, error) {
			return datastore.OpenPath(ctx, dbPath)
		},
		artifactDirectory: func() (string, error) { return artifactDirectory, nil },
		edit:              edit,
		launch:            launch,
		now:               func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) },
		newID:             func() (string, error) { return "handoff-test", nil },
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
				workflow := testWorkflow(t, func(_ kitty.KittyClient, sess config.Session, acc config.Account, _ *config.Config, prompt string) error {
					gotSession, gotAccount, gotPrompt = sess, acc, prompt
					return nil
				}, nil)
				result, err := workflow.Run(context.Background(), Request{
					Source: transcript.SessionMeta{ID: "source-1", Harness: sourceHarness, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
					Reader: workflowReader{harness: sourceHarness, messages: []transcript.Message{{Role: "user", Text: "Continue the migration."}}},
					Config: cfg, TargetProfile: targetProfile,
					Kitty: kitty.NewClientWithSocket(kitty.NewMockRunner(nil), ""),
				})
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				if !result.Launched || result.Artifact.Record.Status != StatusFinal {
					t.Fatalf("result = %+v, want a finalized launched handoff", result)
				}
				if gotSession.Directory != cwd || gotSession.EffectiveProfile() != targetProfile || gotAccount.EffectiveHarness() != targetHarness {
					t.Fatalf("launch context = session=%+v account=%+v, want source cwd and target profile/harness", gotSession, gotAccount)
				}
				if !strings.Contains(gotPrompt, result.Artifact.Record.Path) || !strings.Contains(gotPrompt, "Verify all assumptions") {
					t.Fatalf("prompt = %q, want handoff path and stale-assumption instruction", gotPrompt)
				}
				if info, err := os.Stat(result.Artifact.Record.Path); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("final artifact = %v, %v; want private regular file", info, err)
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
	if result.DraftPath == "" || result.Artifact.Record.Path == "" || result.Launched {
		t.Fatalf("result = %+v, want retained draft and finalized create-only artifact", result)
	}
	final, err := os.ReadFile(result.Artifact.Record.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(final) != "# Edited Handoff\n\nUse current tests.\n" {
		t.Fatalf("final content = %q, want edited draft content", final)
	}
}

func TestWorkflowRetainsFinalArtifactWhenLaunchFails(t *testing.T) {
	cwd := t.TempDir()
	sourcePath := filepath.Join(cwd, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workflow := testWorkflow(t, func(kitty.KittyClient, config.Session, config.Account, *config.Config, string) error {
		return errors.New("Kitty launch failed")
	}, nil)
	result, err := workflow.Run(context.Background(), Request{
		Source:        transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: cwd, Path: sourcePath},
		Reader:        workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Original goal"}}},
		Config:        &config.Config{Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		TargetProfile: "target", Kitty: kitty.NewClientWithSocket(kitty.NewMockRunner(nil), ""),
	})
	var partial *PartialLaunchError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %v, want PartialLaunchError", err)
	}
	if result.Artifact.Record.Status != StatusFinal || result.Artifact.Record.Path == "" {
		t.Fatalf("partial result = %+v, want finalized artifact", result)
	}
	if _, statErr := os.Stat(result.Artifact.Record.Path); statErr != nil {
		t.Fatalf("final artifact was not retained: %v", statErr)
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
	workflow := testWorkflow(t, func(_ kitty.KittyClient, session config.Session, _ config.Account, _ *config.Config, _ string) error {
		launched = session
		return nil
	}, nil)
	_, err := workflow.Run(context.Background(), Request{
		Source:        transcript.SessionMeta{ID: "source-1", Harness: harness.Claude, Profile: "source", Project: "api", CWD: sourceCWD, Path: sourcePath},
		Reader:        workflowReader{harness: harness.Claude, messages: []transcript.Message{{Role: "user", Text: "Original goal"}}},
		Config:        &config.Config{Accounts: []config.Account{{Name: "source", Harness: harness.Claude}, {Name: "target", Harness: harness.Codex}}},
		TargetProfile: "target", LaunchDirectory: launchCWD, Kitty: kitty.NewClientWithSocket(kitty.NewMockRunner(nil), ""),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if launched.Directory != launchCWD {
		t.Fatalf("launch directory = %q, want %q", launched.Directory, launchCWD)
	}
}
