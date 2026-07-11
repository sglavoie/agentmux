package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestDefaultRegistryHasStableDefinitions(t *testing.T) {
	registry := DefaultRegistry()
	got := registry.Definitions()
	want := []ID{OpenDirectory, OpenEditor, ClearStatus, Close, Continue, CopyTitle, Focus, Fork, ForkWorktree, Handoff, Launch, Restart, ToggleLock, ToggleMute, AdoptProject, MarkAllRead, MarkRead}
	if len(got) != len(want) {
		t.Fatalf("definition count = %d, want %d", len(got), len(want))
	}
	for i, definition := range got {
		if definition.ID != want[i] {
			t.Fatalf("definition %d = %q, want %q", i, definition.ID, want[i])
		}
	}
}

type recordingAdopter struct{ project config.Session }

func (a *recordingAdopter) AdoptProject(_ context.Context, project config.Session) (config.AdoptionResult, error) {
	a.project = project
	return config.AdoptionResult{Path: "/config.toml", BackupPath: "/config.toml.bak", Block: "[[projects]]\n"}, nil
}

func TestAdoptProjectRequiresConfirmationAndUsesDefaultTitle(t *testing.T) {
	registry := DefaultRegistry()
	target := ProjectCandidateTarget{ID: "candidate-1", CanonicalPath: "/src/api", DisplayName: "api"}
	request := Request{Candidate: &target, Parameters: map[string]string{"profile": "work"}}
	deps := Dependencies{Adopter: &recordingAdopter{}, Config: &config.Config{Profiles: []config.Account{{Name: "work"}}}}
	_, err := registry.Execute(context.Background(), deps, AdoptProject, request)
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "confirmation_required" {
		t.Fatalf("unconfirmed error = %v, want confirmation_required", err)
	}

	adopter := &recordingAdopter{}
	request.Confirmed = true
	deps.Adopter = adopter
	result, err := registry.Execute(context.Background(), deps, AdoptProject, request)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if adopter.project.Title != "api" || adopter.project.Directory != "/src/api" || adopter.project.Profile != "work" {
		t.Fatalf("adopted project = %+v", adopter.project)
	}
	if !result.Changed || result.Data["candidate_id"] != "candidate-1" {
		t.Fatalf("result = %+v", result)
	}
}

type recordingActivityMarker struct {
	workspaceID string
	readerID    string
	through     time.Time
	allCalls    int
}

func (m *recordingActivityMarker) MarkWorkspaceRead(_ context.Context, workspaceID, readerID string, through time.Time) (bool, error) {
	m.workspaceID, m.readerID, m.through = workspaceID, readerID, through
	return true, nil
}

func (m *recordingActivityMarker) MarkAllWorkspacesRead(_ context.Context, readerID string) (int64, error) {
	m.readerID = readerID
	m.allCalls++
	return 3, nil
}

func TestWorkspaceReadActionsUseTypedLoadedRange(t *testing.T) {
	marker := &recordingActivityMarker{}
	through := time.Unix(1_720_000_000, 0)
	target := WorkspaceTarget{ID: "api", LoadedThrough: through}
	registry := DefaultRegistry()

	result, err := registry.Execute(context.Background(), Dependencies{Activity: marker}, MarkRead, Request{Workspace: &target})
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if !result.Changed || marker.workspaceID != "api" || marker.readerID != "local" || !marker.through.Equal(through) {
		t.Fatalf("mark workspace result=%+v marker=%+v", result, marker)
	}
	result, err = registry.Execute(context.Background(), Dependencies{Activity: marker}, MarkAllRead, Request{})
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if !result.Changed || marker.allCalls != 1 || result.Data["workspaces"] != int64(3) {
		t.Fatalf("mark all result=%+v marker=%+v", result, marker)
	}
}

func TestWorkspaceReadActionRequiresLoadedActivity(t *testing.T) {
	target := WorkspaceTarget{ID: "api"}
	availability := DefaultRegistry().Availability(Dependencies{Activity: &recordingActivityMarker{}}, MarkRead, Request{Workspace: &target})
	if availability.Available || availability.Reason == "" {
		t.Fatalf("availability = %+v, want loaded-activity reason", availability)
	}
}

func TestFocusUsesInjectedKittyClient(t *testing.T) {
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	target := session.RunningSession{TabID: 42}

	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{Kitty: client}, Focus, Request{Session: &target})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Changed || result.Action != Focus {
		t.Fatalf("result = %+v, want changed focus result", result)
	}
	if runner.CallCount("kitten", "@", "focus-tab", "--match=id:42") != 1 {
		t.Fatalf("focus did not use injected client: %+v", runner.Calls)
	}
}

func TestCloseReportsConfirmationAndLockAvailability(t *testing.T) {
	target := session.RunningSession{TabID: 42}
	registry := DefaultRegistry()

	_, err := registry.Execute(context.Background(), Dependencies{}, Close, Request{Session: &target})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "confirmation_required" {
		t.Fatalf("error = %v, want confirmation_required", err)
	}

	target.Locked = true
	_, err = registry.Execute(context.Background(), Dependencies{}, Close, Request{Session: &target, Confirmed: true})
	if !errors.As(err, &actionErr) || actionErr.Code != "unavailable" {
		t.Fatalf("locked error = %v, want unavailable", err)
	}
}

func TestCopyTitleUsesInjectedClipboard(t *testing.T) {
	var copied string
	target := session.RunningSession{Project: "api", Base: "W:api"}
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Clipboard: func(text string) error {
			copied = text
			return nil
		},
	}, CopyTitle, Request{Session: &target})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if copied != "api" || result.Data["title"] != "api" {
		t.Fatalf("copy result = %+v, copied = %q", result, copied)
	}
}
