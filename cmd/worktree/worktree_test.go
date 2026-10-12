package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

type commandWorktreeManager struct {
	entries       []worktree.Entry
	removeOptions []worktree.RemoveOptions
	forgetCalls   int
	reviewOptions []worktree.ReviewOptions
	review        worktree.Review
}

func (m *commandWorktreeManager) List(context.Context) ([]worktree.Entry, error) {
	return append([]worktree.Entry(nil), m.entries...), nil
}
func (m *commandWorktreeManager) Resolve(_ context.Context, selector string) (worktree.Entry, error) {
	return worktree.ResolveSelector(m.entries, selector)
}
func (m *commandWorktreeManager) Remove(_ context.Context, _ string, options worktree.RemoveOptions) (worktree.MutationResult, error) {
	m.removeOptions = append(m.removeOptions, options)
	return worktree.MutationResult{Entry: m.entries[0], CheckoutRemoved: true, BranchDeleted: options.DeleteBranch, MetadataForgotten: true}, nil
}
func (m *commandWorktreeManager) Review(_ context.Context, _ worktree.Entry, options worktree.ReviewOptions) (worktree.Review, error) {
	m.reviewOptions = append(m.reviewOptions, options)
	return m.review, nil
}
func (m *commandWorktreeManager) Forget(context.Context, string) (worktree.MutationResult, error) {
	m.forgetCalls++
	return worktree.MutationResult{Entry: m.entries[0], MetadataForgotten: true}, nil
}

func TestWorktreeCLITextAndJSONContracts(t *testing.T) {
	manager, client := resetWorktreeCommandsTestState(t)
	commands := []*struct {
		command *cobra.Command
		args    []string
	}{
		{worktreeListCmd, nil},
		{worktreeShowCmd, []string{"wt-1"}},
		{worktreeDiffCmd, []string{"wt-1"}},
		{worktreeReopenCmd, []string{"wt-1"}},
		{worktreeShellCmd, []string{"wt-1"}},
		{worktreeEditCmd, []string{"wt-1"}},
		{worktreeRemoveCmd, []string{"wt-1"}},
		{worktreeForgetCmd, []string{"wt-1"}},
	}
	_ = client
	for _, tc := range commands {
		var out bytes.Buffer
		tc.command.SetOut(&out)
		if tc.command == worktreeRemoveCmd || tc.command == worktreeForgetCmd {
			if err := tc.command.Flags().Set("yes", "true"); err != nil {
				t.Fatal(err)
			}
		}
		if tc.command == worktreeRemoveCmd {
			worktreeCommandsLiveSessions = func(kitty.KittyClient, *config.Config) ([]session.RunningSession, error) { return nil, nil }
			if err := tc.command.Flags().Set("delete-branch", "true"); err != nil {
				t.Fatal(err)
			}
		}
		if err := tc.command.Flags().Set("json", "true"); err != nil {
			t.Fatal(err)
		}
		if err := tc.command.RunE(tc.command, tc.args); err != nil {
			t.Fatalf("%s JSON: %v", tc.command.Name(), err)
		}
		var envelope struct {
			SchemaVersion int             `json:"schema_version"`
			Data          json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.SchemaVersion != cli.JSONSchemaVersion || len(envelope.Data) == 0 {
			t.Fatalf("%s JSON = %q envelope=%+v err=%v", tc.command.Name(), out.String(), envelope, err)
		}
	}
	if len(manager.removeOptions) != 1 || !manager.removeOptions[0].DeleteBranch || manager.forgetCalls != 1 {
		t.Fatalf("cleanup calls = remove %+v forget %d", manager.removeOptions, manager.forgetCalls)
	}
}

func TestWorktreeCLIListAndShowTextContracts(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	manager.entries[0].Warnings = []string{"status snapshot is stale"}

	var out bytes.Buffer
	worktreeListCmd.SetOut(&out)
	if err := worktreeListCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	if err := worktreeListCmd.RunE(worktreeListCmd, nil); err != nil {
		t.Fatalf("list text: %v", err)
	}
	wantRow := strings.Join([]string{
		manager.entries[0].ID,
		manager.entries[0].ProjectLabel,
		manager.entries[0].Branch,
		"active",
		manager.entries[0].Path,
	}, "\t")
	if got := strings.TrimSpace(out.String()); got != wantRow {
		t.Fatalf("list text = %q, want %q", got, wantRow)
	}

	out.Reset()
	worktreeShowCmd.SetOut(&out)
	if err := worktreeShowCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	if err := worktreeShowCmd.RunE(worktreeShowCmd, []string{manager.entries[0].ID}); err != nil {
		t.Fatalf("show text: %v", err)
	}
	for _, want := range []string{
		"id: wt-1\n",
		"project: api\n",
		"live_sessions: 1\n",
		"warning: status snapshot is stale\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("show text = %q, want substring %q", out.String(), want)
		}
	}
}

func TestWorktreeCLITextAndJSONFailuresShareExitStatus(t *testing.T) {
	resetWorktreeCommandsTestState(t)

	if err := worktreeListCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	textErr := worktreeListCmd.RunE(worktreeListCmd, []string{"unexpected"})

	var out bytes.Buffer
	worktreeListCmd.SetOut(&out)
	if err := worktreeListCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	jsonErr := worktreeListCmd.RunE(worktreeListCmd, []string{"unexpected"})
	payload := decodeWorktreeJSONError(t, out.Bytes(), jsonErr)
	if payload.Error.Code != "invalid_arguments" {
		t.Fatalf("JSON error = %+v", payload.Error)
	}

	textStatus := worktreeCommandExitStatus(textErr)
	jsonStatus := worktreeCommandExitStatus(jsonErr)
	if textStatus != jsonStatus || textStatus != 1 {
		t.Fatalf("exit statuses: text=%d JSON=%d, want both 1", textStatus, jsonStatus)
	}
}

func TestWorktreeSelectorErrorsPointAtTheList(t *testing.T) {
	resetWorktreeCommandsTestState(t)
	if err := worktreeShowCmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}

	err := worktreeShowCmd.RunE(worktreeShowCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "`amx worktree list`") {
		t.Fatalf("missing selector error = %v, want a pointer at `amx worktree list`", err)
	}

	err = worktreeShowCmd.RunE(worktreeShowCmd, []string{"no-such-worktree"})
	if err == nil || !strings.Contains(err.Error(), `no managed worktree matches "no-such-worktree"; `+worktreeListHint) {
		t.Fatalf("unknown selector error = %v, want the list hint", err)
	}

	var out bytes.Buffer
	worktreeShowCmd.SetOut(&out)
	if err := worktreeShowCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	err = worktreeShowCmd.RunE(worktreeShowCmd, []string{"no-such-worktree"})
	payload := decodeWorktreeJSONError(t, out.Bytes(), err)
	// As the session and project commands do, the JSON message carries the
	// same pointer, for a caller that relays it to a person.
	if payload.Error.Code != "not_found" || !strings.HasSuffix(payload.Error.Message, "; "+worktreeListHint) {
		t.Fatalf("JSON not-found payload = %+v, want the list hint", payload.Error)
	}
}

func TestWorktreeTextHelpers(t *testing.T) {
	view := actions.WorktreeView{Entry: worktree.Entry{
		ID: "wt-1", ProjectLabel: "api", GitAvailable: true,
	}}
	if got := displayWorktreeProject(view); got != "api" {
		t.Fatalf("displayWorktreeProject() = %q, want api", got)
	}
	if got := worktreeState(view); got != "clean" {
		t.Fatalf("worktreeState() = %q, want clean", got)
	}

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)
	printWorktree(command, view)
	writeWorktreeWarnings(command, []string{"probe unavailable"})
	if !strings.Contains(out.String(), "project: api\n") || !strings.Contains(out.String(), "warning: probe unavailable\n") {
		t.Fatalf("helper text = %q", out.String())
	}
}

func TestWorktreeCLIMapsCandidatesConfirmationsAndParameters(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	other := manager.entries[0]
	other.ID = "wt-2"
	other.Path = filepath.Join(filepath.Dir(other.Path), "other")
	other.Branch = manager.entries[0].Branch
	manager.entries = append(manager.entries, other)

	var out bytes.Buffer
	worktreeShowCmd.SetOut(&out)
	if err := worktreeShowCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	err := worktreeShowCmd.RunE(worktreeShowCmd, []string{manager.entries[0].Branch})
	payload := decodeWorktreeJSONError(t, out.Bytes(), err)
	if payload.Error.Code != "ambiguous_selector" || len(payload.Error.Candidates) != 2 {
		t.Fatalf("ambiguous payload = %+v", payload)
	}

	out.Reset()
	worktreeRemoveCmd.SetOut(&out)
	if err := worktreeRemoveCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	err = worktreeRemoveCmd.RunE(worktreeRemoveCmd, []string{"wt-1"})
	payload = decodeWorktreeJSONError(t, out.Bytes(), err)
	if payload.Error.Code != "confirmation_required" || len(payload.Error.Parameters) != 1 || payload.Error.Parameters[0].Name != "yes" || len(manager.removeOptions) != 0 {
		t.Fatalf("confirmation payload = %+v remove=%v", payload, manager.removeOptions)
	}

	out.Reset()
	worktreeReopenCmd.SetOut(&out)
	if err := worktreeReopenCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	worktreeCommandsLiveSessions = func(kitty.KittyClient, *config.Config) ([]session.RunningSession, error) { return nil, nil }
	worktreeCommandsTranscriptIndex = func() (*transcript.Index, error) { return nil, errors.New("cache unavailable") }
	err = worktreeReopenCmd.RunE(worktreeReopenCmd, []string{"wt-1"})
	payload = decodeWorktreeJSONError(t, out.Bytes(), err)
	if payload.Error.Code != "probe_unavailable" {
		t.Fatalf("reopen unavailable payload = %+v", payload)
	}
}

// The half of this assertion that named rootCmd moved into
// TestCommandFamiliesAreRegisteredUnderRoot in cmd/root_test.go: proving the
// family is reachable as `amx worktree` is only true once root has registered
// it, and asserting that against a locally built parent would be tautological.
func TestWorktreeCLIHelpIsRegistered(t *testing.T) {
	for _, name := range []string{"list", "show", "diff", "reopen", "shell", "edit", "remove", "forget"} {
		if command, _, err := worktreeCmd.Find([]string{name}); err != nil || command == nil || command.Name() != name {
			t.Fatalf("worktree %s registration: command=%v err=%v", name, command, err)
		}
	}
}

func resetWorktreeCommandsTestState(t *testing.T) (*commandWorktreeManager, *worktreeCommandKitty) {
	t.Helper()
	oldLoadConfig := worktreeCommandsLoadConfig
	oldNewManager := worktreeCommandsNewManager
	oldNewKitty := worktreeCommandsNewKitty
	oldLive := worktreeCommandsLiveSessions
	oldLoops := worktreeCommandsActiveLoops
	oldIndex := worktreeCommandsTranscriptIndex
	oldReaders := worktreeCommandsReaders
	oldIsTerminal := worktreeListIsTerminal
	commands := []*cobra.Command{worktreeListCmd, worktreeShowCmd, worktreeDiffCmd, worktreeReopenCmd, worktreeShellCmd, worktreeEditCmd, worktreeRemoveCmd, worktreeForgetCmd}
	outputs := make([]io.Writer, len(commands))
	for index, command := range commands {
		outputs[index] = command.OutOrStdout()
	}
	t.Cleanup(func() {
		worktreeCommandsLoadConfig = oldLoadConfig
		worktreeCommandsNewManager = oldNewManager
		worktreeCommandsNewKitty = oldNewKitty
		worktreeCommandsLiveSessions = oldLive
		worktreeCommandsActiveLoops = oldLoops
		worktreeCommandsTranscriptIndex = oldIndex
		worktreeCommandsReaders = oldReaders
		worktreeListIsTerminal = oldIsTerminal
		for index, command := range commands {
			command.SetOut(outputs[index])
			_ = command.Flags().Set("json", "false")
		}
		_ = worktreeReopenCmd.Flags().Set("session", "")
		_ = worktreeReopenCmd.Flags().Set("profile", "")
		_ = worktreeRemoveCmd.Flags().Set("yes", "false")
		_ = worktreeRemoveCmd.Flags().Set("delete-branch", "false")
		_ = worktreeForgetCmd.Flags().Set("yes", "false")
		_ = worktreeDiffCmd.Flags().Set("committed", "false")
		_ = worktreeDiffCmd.Flags().Set("patch", "false")
	})

	root := t.TempDir()
	source := t.TempDir()
	entry := worktree.Entry{
		ID: "wt-1", Classification: worktree.ClassificationValid, ProjectLabel: "api", Path: root,
		Branch: "amx/api/source-1", SourceRoot: source, SourceBranch: "main", Profile: "work", Harness: harness.Claude,
		Exists: true, GitAvailable: true, StatusAvailable: true, Registered: true, Actionable: true,
	}
	manager := &commandWorktreeManager{entries: []worktree.Entry{entry}}
	client := &worktreeCommandKitty{}
	cfg := &config.Config{
		Defaults: config.Defaults{Editor: "true"},
		Accounts: []config.Account{{Name: "work", Harness: harness.Claude, Color: "blue"}},
		Projects: []config.Session{{Title: "api", Directory: source, Profile: "work"}},
	}
	worktreeCommandsLoadConfig = func() (*config.Config, error) { return cfg, nil }
	worktreeCommandsNewManager = func() actions.WorktreeLifecycle { return manager }
	worktreeCommandsNewKitty = func() kitty.KittyClient { return client }
	worktreeCommandsLiveSessions = func(kitty.KittyClient, *config.Config) ([]session.RunningSession, error) {
		return []session.RunningSession{{TabID: 42, WindowID: 420, Project: "api", Profile: "work", Harness: harness.Claude, Directory: root}}, nil
	}
	worktreeCommandsActiveLoops = func() ([]agentloop.Candidate, error) { return nil, nil }
	return manager, client
}

func decodeWorktreeJSONError(t *testing.T, body []byte, err error) cli.JSONErrorEnvelope {
	t.Helper()
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || !exitErr.Quiet {
		t.Fatalf("error = %#v, want quiet cli.ExitError", err)
	}
	var payload cli.JSONErrorEnvelope
	if decodeErr := json.Unmarshal(body, &payload); decodeErr != nil {
		t.Fatalf("JSON error = %v\n%s", decodeErr, string(body))
	}
	return payload
}

func worktreeCommandExitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *cli.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}

func TestCompleteWorktreeSelectorsOffersIDsWithBranchAndProject(t *testing.T) {
	old := worktreeCommandsNewManager
	t.Cleanup(func() { worktreeCommandsNewManager = old })
	manager := &commandWorktreeManager{entries: []worktree.Entry{
		{ID: "wt-api", Branch: "amx/fix", ProjectLabel: "API"},
		{ID: "wt-bare"},
		{ID: "other"},
	}}
	worktreeCommandsNewManager = func() actions.WorktreeLifecycle { return manager }

	got, _ := completeWorktreeSelectors(worktreeShowCmd, nil, "wt-")
	if want := []string{"wt-api\tamx/fix API", "wt-bare"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completions = %q, want %q", got, want)
	}
	if got, _ := completeWorktreeSelectors(worktreeShowCmd, []string{"wt-api"}, ""); got != nil {
		t.Fatalf("second argument completed %q, want nothing", got)
	}
	for _, command := range []*cobra.Command{worktreeShowCmd, worktreeDiffCmd, worktreeShellCmd, worktreeEditCmd, worktreeReopenCmd, worktreeRemoveCmd, worktreeForgetCmd} {
		if command.ValidArgsFunction == nil {
			t.Errorf("worktree %s has no selector completion", command.Name())
		}
	}
}

func TestWorktreeDiffTextNamesTheVerdictFilesAndConflicts(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	manager.review = worktree.Review{
		ID: "wt-1", Branch: "amx/api/source-1", SourceBranch: "main", Ahead: 2, Behind: 1, Dirty: true,
		Files: []worktree.FileChange{{Path: "a.go", Added: 3, Deleted: 1}, {Path: "img/logo.png", Binary: true}},
		Added: 3, Deleted: 1, Untracked: []string{"notes.md"},
		Lands: worktree.LandsConflicts, Conflicts: []string{"a.go"}, Patch: "diff --git a/a.go b/a.go\n",
	}
	var out bytes.Buffer
	worktreeDiffCmd.SetOut(&out)
	for _, flag := range []string{"committed", "patch"} {
		if err := worktreeDiffCmd.Flags().Set(flag, "true"); err != nil {
			t.Fatal(err)
		}
	}
	if err := worktreeDiffCmd.RunE(worktreeDiffCmd, []string{"wt-1"}); err != nil {
		t.Fatalf("diff text: %v", err)
	}
	want := "amx/api/source-1 against main: 2 commits ahead, 1 behind, uncommitted changes\n" +
		"lands: conflicts with main in 1 file\n" +
		" a.go         | +3 -1\n" +
		" img/logo.png | binary\n" +
		"2 files changed, +3 -1\n" +
		"untracked: notes.md\n" +
		"conflict: a.go\n" +
		"\ndiff --git a/a.go b/a.go\n"
	if out.String() != want {
		t.Fatalf("diff text =\n%s\nwant\n%s", out.String(), want)
	}
	// A buffer is no terminal, so the patch is asked for without color.
	if want := []worktree.ReviewOptions{{CommittedOnly: true, Patch: true}}; !reflect.DeepEqual(manager.reviewOptions, want) {
		t.Fatalf("review options = %+v, want %+v", manager.reviewOptions, want)
	}
}

func TestWorktreeDiffRefusesAWorktreeWithNoRecordedSource(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	manager.entries[0].SourceBranch = ""
	var out bytes.Buffer
	worktreeDiffCmd.SetOut(&out)
	if err := worktreeDiffCmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	err := worktreeDiffCmd.RunE(worktreeDiffCmd, []string{"wt-1"})
	if payload := decodeWorktreeJSONError(t, out.Bytes(), err); payload.Error.Code != "unavailable" {
		t.Fatalf("error = %+v, want unavailable", payload.Error)
	}
}
