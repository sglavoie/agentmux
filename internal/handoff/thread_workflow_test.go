package handoff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestHandoffWorkflowRecordsOnlySuccessfulLaunches(t *testing.T) {
	workflow := testWorkflow(t, nil, nil)
	store := conversation.ThreadStore{Path: filepath.Join(t.TempDir(), "links.json")}
	workflow.threads = func() (conversation.ThreadStore, error) { return store, nil }
	launches := 0
	workflow.launch = func(session.Runtime, config.Session, config.Account, *config.Config, string) (session.LaunchResult, error) {
		launches++
		return session.LaunchResult{RuntimeSessionID: runtimesession.ID("rs-one")}, nil
	}
	cwd := t.TempDir()
	request := Request{Source: transcript.SessionMeta{ID: "source", Harness: "claude", CWD: cwd, Path: filepath.Join(cwd, "source.jsonl")},
		Reader: workflowReader{harness: "claude"}, Content: []byte("Reviewed context"),
		Config: &config.Config{Accounts: []config.Account{{Name: "review", Harness: "codex"}}}, TargetProfile: "review", Runtime: session.Runtime{Tmux: selectedTmux{}}}
	request.Preview = true
	if _, err := workflow.Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Preview = false
	request.NoLaunch = true
	if _, err := workflow.Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path); !os.IsNotExist(err) {
		t.Fatal("preview or no-launch recorded a link")
	}
	request.NoLaunch = false
	result, err := workflow.Run(context.Background(), request)
	if err != nil || !result.Launched || launches != 1 {
		t.Fatalf("handoff: %+v %v", result, err)
	}
	links, err := store.Links()
	key := "rs-one"
	if err != nil || len(links) != 1 || links[0].TargetKey != key || links[0].Source.SessionID != "source" || links[0].ArtifactPath != result.Artifact.Path {
		t.Fatalf("missing source link: %+v %v", links, err)
	}
	workflow.newID = func() (string, error) { return "failed-handoff", nil }
	workflow.launch = func(session.Runtime, config.Session, config.Account, *config.Config, string) (session.LaunchResult, error) {
		return session.LaunchResult{}, errors.New("refused launch")
	}
	if _, err := workflow.Run(context.Background(), request); err == nil {
		t.Fatal("expected launch failure")
	}
	links, _ = store.Links()
	if len(links) != 1 {
		t.Fatal("failed launch recorded a continuation")
	}
	workflow.launch = func(session.Runtime, config.Session, config.Account, *config.Config, string) (session.LaunchResult, error) {
		if err := os.WriteFile(store.Path, []byte("history damaged during launch"), 0o600); err != nil {
			t.Fatal(err)
		}
		return session.LaunchResult{RuntimeSessionID: runtimesession.ID("rs-new")}, nil
	}
	launched, err := workflow.Run(context.Background(), request)
	var relationship *RelationshipError
	if !errors.As(err, &relationship) || !launched.Launched || launched.RuntimeSessionID != "rs-new" {
		t.Fatalf("lost successful launch on recording failure: %+v %v", launched, err)
	}
	workflow.launch = func(session.Runtime, config.Session, config.Account, *config.Config, string) (session.LaunchResult, error) {
		t.Fatal("corrupt history should be reported before launching another agent")
		return session.LaunchResult{}, nil
	}
	if _, err := workflow.Run(context.Background(), request); err == nil {
		t.Fatal("corrupt history was ignored")
	}

}
