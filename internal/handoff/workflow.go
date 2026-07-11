package handoff

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

// Request describes one portable continuation. Source is always an indexed
// conversation; TargetProfile selects the configured harness/profile that
// receives a completely fresh session.
type Request struct {
	Source        transcript.SessionMeta
	Reader        transcript.Reader
	Config        *config.Config
	TargetProfile string
	// LaunchDirectory overrides only the fresh target's working directory.
	// The source CWD remains the provenance and pack-building context. It is
	// used by isolated worktree forks so portable fallbacks run in the new
	// checkout rather than silently returning to the source tree.
	LaunchDirectory string
	Edit            bool
	NoLaunch        bool
	Label           string
	Kitty           kitty.KittyClient
}

// Result describes the finalized handoff and, when requested, the fresh
// target session. Artifact is retained for both complete and partial runs.
type Result struct {
	Artifact      Artifact
	DraftPath     string
	TargetProfile string
	TargetHarness string
	Launched      bool
	Label         string
}

// Runner permits action and command callers to substitute a deterministic
// handoff implementation without reaching into package globals.
type Runner interface {
	Run(context.Context, Request) (Result, error)
}

// PartialLaunchError means finalization succeeded and the artifact path in
// Result remains safe to use, but creating the target Kitty tab failed.
type PartialLaunchError struct{ Err error }

func (e *PartialLaunchError) Error() string {
	if e == nil || e.Err == nil {
		return "handoff pack was finalized but target launch failed"
	}
	return "handoff pack was finalized but target launch failed: " + e.Err.Error()
}

func (e *PartialLaunchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Workflow turns an indexed source into a finalized handoff artifact, then
// starts a fresh target harness instructed to read it. The small dependency
// seams keep the real filesystem/Kitty path testable without global state.
type Workflow struct {
	builder           *Builder
	openStore         func(context.Context) (*datastore.Store, error)
	artifactDirectory func() (string, error)
	edit              func(string, *config.Config) error
	launch            func(kitty.KittyClient, config.Session, config.Account, *config.Config, string) error
	now               func() time.Time
	newID             func() (string, error)
}

func NewWorkflow() *Workflow {
	return &Workflow{
		builder:           NewBuilder(),
		openStore:         datastore.Open,
		artifactDirectory: DefaultDirectory,
		edit:              session.EditPath,
		launch:            session.LaunchFresh,
		now:               time.Now,
		newID:             newArtifactID,
	}
}

// Run writes a draft only for the explicit edit flow, always writes a final
// artifact before launch, and never removes that final artifact after a launch
// failure. It intentionally launches in Source.CWD instead of a target
// profile's configured directory, preserving the source worktree.
func (w *Workflow) Run(ctx context.Context, request Request) (Result, error) {
	if w == nil {
		w = NewWorkflow()
	}
	if request.Config == nil {
		return Result{}, errors.New("handoff: configuration is required")
	}
	if request.Reader == nil {
		return Result{}, errors.New("handoff: source transcript reader is required")
	}
	if strings.TrimSpace(request.Source.Harness) == "" || strings.TrimSpace(request.Source.ID) == "" {
		return Result{}, errors.New("handoff: source conversation must include harness and session ID")
	}
	if !strings.EqualFold(request.Reader.Harness(), request.Source.Harness) {
		return Result{}, fmt.Errorf("handoff: transcript reader %q cannot read source harness %q", request.Reader.Harness(), request.Source.Harness)
	}
	if strings.TrimSpace(request.Source.Path) == "" {
		return Result{}, errors.New("handoff: source conversation has no transcript path")
	}
	if strings.TrimSpace(request.Source.CWD) == "" {
		return Result{}, errors.New("handoff: source conversation has no working directory")
	}
	if !request.NoLaunch && request.Kitty == nil {
		return Result{}, errors.New("handoff: Kitty client is required when launching a target session")
	}

	target, ok := accountByName(request.Config, request.TargetProfile)
	if !ok {
		return Result{}, fmt.Errorf("handoff: no matching target profile %q", request.TargetProfile)
	}
	messages, err := request.Reader.ReadMessages(request.Source.Path)
	if err != nil {
		return Result{}, fmt.Errorf("handoff: reading source transcript: %w", err)
	}

	store, err := w.openStore(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("handoff: opening datastore: %w", err)
	}
	defer func() { _ = store.Close() }()

	workspaceID, workspaceName, linkedPlans, conversationID, err := handoffContext(ctx, store, request.Config, request.Source)
	if err != nil {
		return Result{}, err
	}
	goal := sourceGoal(request.Source, messages)
	pack, err := w.builder.Build(ctx, Source{
		WorkspaceID: workspaceID, WorkspaceName: workspaceName,
		Harness: request.Source.Harness, SessionID: request.Source.ID, ConversationID: conversationID,
		CapturedAt: w.now().UTC(), Goal: goal, CWD: request.Source.CWD,
		TranscriptPath: request.Source.Path, Messages: messages, LinkedPlans: linkedPlans,
	}, Options{
		MaxBytes:       request.Config.Defaults.Handoff.MaxBytes,
		RecentMessages: request.Config.Defaults.Handoff.RecentMessages,
	})
	if err != nil {
		return Result{}, err
	}

	id, err := w.newID()
	if err != nil {
		return Result{}, fmt.Errorf("handoff: creating artifact ID: %w", err)
	}
	directory, err := w.artifactDirectory()
	if err != nil {
		return Result{}, err
	}
	repository, err := NewRepository(store)
	if err != nil {
		return Result{}, err
	}
	artifacts, err := NewArtifactStore(directory, repository)
	if err != nil {
		return Result{}, err
	}
	title := handoffTitle(request.Source, target.Name, request.Label)
	writeRequest := func(status string, content []byte) WriteRequest {
		now := w.now().UTC()
		return WriteRequest{
			ID: id, Status: status, WorkspaceID: workspaceID, ConversationID: conversationID,
			Title: title, Content: content, CreatedAt: now, UpdatedAt: now,
		}
	}

	result := Result{TargetProfile: target.Name, TargetHarness: target.EffectiveHarness(), Label: request.Label}
	content := pack.Content
	if request.Edit {
		draft, err := artifacts.Write(ctx, writeRequest(StatusDraft, content))
		if err != nil {
			return Result{}, err
		}
		result.DraftPath = draft.Record.Path
		if err := w.edit(draft.Record.Path, request.Config); err != nil {
			return result, fmt.Errorf("handoff: editing draft %s: %w", draft.Record.Path, err)
		}
		content, err = os.ReadFile(draft.Record.Path)
		if err != nil {
			return result, fmt.Errorf("handoff: reading edited draft %s: %w", draft.Record.Path, err)
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			return result, errors.New("handoff: edited draft is empty")
		}
	}

	final, err := artifacts.Write(ctx, writeRequest(StatusFinal, content))
	if err != nil {
		return result, err
	}
	result.Artifact = final
	if request.NoLaunch {
		return result, nil
	}

	launchSession := targetSession(request.Config, request.Source, target)
	if directory := strings.TrimSpace(request.LaunchDirectory); directory != "" {
		info, err := os.Stat(directory)
		if err != nil {
			return result, fmt.Errorf("handoff: checking target launch directory: %w", err)
		}
		if !info.IsDir() {
			return result, fmt.Errorf("handoff: target launch directory %q is not a directory", directory)
		}
		launchSession.Directory = directory
	}
	prompt := launchPrompt(final.Record.Path)
	if err := w.launch(request.Kitty, launchSession, target, request.Config, prompt); err != nil {
		return result, &PartialLaunchError{Err: err}
	}
	result.Launched = true
	return result, nil
}

func handoffContext(ctx context.Context, store *datastore.Store, cfg *config.Config, source transcript.SessionMeta) (string, string, []plans.PlanMeta, *int64, error) {
	conversationID, err := conversationRowID(ctx, store, source.Harness, source.ID)
	if err != nil {
		return "", "", nil, nil, err
	}
	match, found, err := workspace.New(store).MatchCWD(ctx, cfg, source.CWD)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("handoff: resolving source workspace: %w", err)
	}
	if !found {
		return "", "", nil, conversationID, nil
	}
	planRepo, err := plans.NewRepository(store)
	if err != nil {
		return "", "", nil, nil, err
	}
	linked, err := planRepo.ListByWorkspace(ctx, match.Workspace.ID)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("handoff: loading linked plans: %w", err)
	}
	return match.Workspace.ID, match.Workspace.DisplayName, linked, conversationID, nil
}

func conversationRowID(ctx context.Context, store *datastore.Store, harnessName, sessionID string) (*int64, error) {
	var id int64
	err := store.DB().QueryRowContext(ctx, `SELECT id FROM conversations WHERE harness = ? AND session_id = ?`, harnessName, sessionID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("handoff: resolving source conversation metadata: %w", err)
	}
	return &id, nil
}

func sourceGoal(source transcript.SessionMeta, messages []transcript.Message) string {
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			if text, ok := message.TextForHandoff(); ok {
				return text
			}
		}
	}
	if project := strings.TrimSpace(source.Project); project != "" {
		return "Continue the indexed conversation for " + project + "."
	}
	return "Continue the indexed conversation."
}

func targetSession(cfg *config.Config, source transcript.SessionMeta, target config.Account) config.Session {
	projects := cfg.Projects
	if len(projects) == 0 {
		projects = cfg.Sessions
	}
	var fallback *config.Session
	for i := range projects {
		project := projects[i]
		if !strings.EqualFold(project.EffectiveProfile(), target.Name) || !sameDirectory(project.Directory, source.CWD) {
			continue
		}
		if strings.EqualFold(project.Title, source.Project) {
			project.Directory, project.Profile, project.Account = source.CWD, target.Name, target.Name
			return project
		}
		if fallback == nil {
			copy := project
			fallback = &copy
		}
	}
	if fallback != nil {
		fallback.Directory, fallback.Profile, fallback.Account = source.CWD, target.Name, target.Name
		if strings.TrimSpace(source.Project) != "" {
			fallback.Title = source.Project
		}
		return *fallback
	}
	title := strings.TrimSpace(source.Project)
	if title == "" {
		title = source.ID
	}
	return config.Session{Title: title, Directory: source.CWD, Profile: target.Name, Account: target.Name}
}

func sameDirectory(left, right string) bool {
	left = filepath.Clean(config.ExpandTilde(left))
	right = filepath.Clean(config.ExpandTilde(right))
	return left != "." && left == right
}

func accountByName(cfg *config.Config, profile string) (config.Account, bool) {
	if cfg == nil {
		return config.Account{}, false
	}
	accounts := cfg.Accounts
	if len(accounts) == 0 {
		accounts = cfg.Profiles
	}
	for _, account := range accounts {
		if strings.EqualFold(account.Name, strings.TrimSpace(profile)) {
			return account, true
		}
	}
	return config.Account{}, false
}

func handoffTitle(source transcript.SessionMeta, targetProfile, label string) string {
	title := strings.TrimSpace(source.Project)
	if title == "" {
		title = source.Harness + ":" + source.ID
	}
	if label = strings.TrimSpace(label); label != "" {
		return label + ": " + title + " → " + targetProfile
	}
	return "Handoff: " + title + " → " + targetProfile
}

func launchPrompt(path string) string {
	return fmt.Sprintf("Read the handoff pack at %q before taking action. Verify all assumptions, repository state, and stale details yourself; the pack may be stale.", path)
}

func newArtifactID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "handoff-" + hex.EncodeToString(bytes), nil
}
