package handoff

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// Request describes one portable continuation. Source is always an indexed
// conversation; TargetProfile selects the configured harness/profile that
// receives a completely fresh session.
type Request struct {
	Source        transcript.SessionMeta
	Reader        transcript.Reader
	Config        *config.Config
	TargetProfile string
	// Preview builds context without writing artifacts or launching.
	Preview bool
	// Content is an explicitly reviewed pack. Nil generates from the transcript.
	Content     []byte
	Instruction string
	// LaunchDirectory overrides only the fresh target's working directory.
	// The source CWD remains the provenance and pack-building context. It is
	// used by isolated worktree forks so portable fallbacks run in the new
	// checkout rather than silently returning to the source tree.
	LaunchDirectory string
	Edit            bool
	NoLaunch        bool
	Label           string
	// Runtime selects the tmux backend the target launches on.
	Runtime session.Runtime
}

// launchRuntime is the backend the target launches on, or false when none.
func (r Request) launchRuntime() (session.Runtime, bool) {
	if r.Runtime.Tmux != nil {
		return r.Runtime, true
	}
	return session.Runtime{}, false
}

// Result describes the finalized handoff and, when requested, the fresh
// target session. Artifact is retained for both complete and partial runs.
type Result struct {
	Artifact         Artifact
	DraftPath        string
	TargetProfile    string
	TargetHarness    string
	Launched         bool
	Label            string
	Content          string
	SourceHarness    string
	SourceSessionID  string
	Directory        string
	RuntimeSessionID string
}

// Runner permits action and command callers to substitute a deterministic
// handoff implementation without reaching into package globals.
type Runner interface {
	Run(context.Context, Request) (Result, error)
}

// PartialLaunchError means finalization succeeded and the artifact path in
// Result remains safe to use, but starting the receiving agent failed.
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
// seams keep the filesystem and tmux launch path testable without global state.
type Workflow struct {
	builder           *Builder
	artifactDirectory func() (string, error)
	edit              func(string, *config.Config) error
	launch            func(session.Runtime, config.Session, config.Account, *config.Config, string) (session.LaunchResult, error)
	now               func() time.Time
	newID             func() (string, error)
	threads           func() (conversation.ThreadStore, error)
}

func NewWorkflow() *Workflow {
	return &Workflow{
		builder:           NewBuilder(),
		artifactDirectory: DefaultDirectory,
		edit:              session.EditPath,
		launch:            launchFresh,
		now:               time.Now,
		newID:             newArtifactID,
		threads:           conversation.DefaultThreadStore,
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
	runtime, canLaunch := request.launchRuntime()
	if !request.Preview && !request.NoLaunch && !canLaunch {
		return Result{}, errors.New("handoff: terminal runtime is required when launching a target session")
	}

	target, ok := accountByName(request.Config, request.TargetProfile)
	if !ok && !request.Preview {
		return Result{}, fmt.Errorf("handoff: no matching target profile %q", request.TargetProfile)
	}
	createdAt := w.now().UTC()
	content := request.Content
	if content == nil {
		messages, err := request.Reader.ReadMessages(request.Source.Path)
		if err != nil {
			return Result{}, fmt.Errorf("handoff: reading source transcript: %w", err)
		}
		pack, err := w.builder.Build(ctx, Source{
			ProjectLabel: projectLabel(request.Source), Harness: request.Source.Harness, SessionID: request.Source.ID,
			CapturedAt: createdAt, CWD: request.Source.CWD, TranscriptPath: request.Source.Path, Messages: messages,
		}, Options{MaxBytes: request.Config.Defaults.Handoff.MaxBytes, RecentMessages: request.Config.Defaults.Handoff.RecentMessages})
		if err != nil {
			return Result{}, err
		}
		content = pack.Content
	}
	if (request.Content != nil && (len(content) > MaxReviewedBytes || !utf8.Valid(content))) || strings.TrimSpace(string(content)) == "" {
		return Result{}, errors.New("handoff: reviewed context must be non-empty UTF-8 and at most 1 MiB")
	}
	if len(request.Instruction) > MaxInstructionBytes {
		return Result{}, errors.New("handoff: instruction exceeds 16 KiB")
	}
	result := Result{Label: request.Label, SourceHarness: request.Source.Harness, SourceSessionID: request.Source.ID, Directory: request.Source.CWD}
	if ok {
		result.TargetProfile, result.TargetHarness = target.Name, target.EffectiveHarness()
	}
	if request.Preview {
		result.Content = string(content)
		return result, nil
	}
	if instruction := strings.TrimSpace(request.Instruction); instruction != "" {
		content = append(append([]byte(nil), content...), []byte("\n\n## Next instruction\n\n"+instruction+"\n")...)
	}

	id, err := w.newID()
	if err != nil {
		return Result{}, fmt.Errorf("handoff: creating artifact ID: %w", err)
	}
	directory, err := w.artifactDirectory()
	if err != nil {
		return Result{}, err
	}
	artifacts, err := NewArtifactStore(directory)
	if err != nil {
		return Result{}, err
	}
	writeRequest := func(status string, content []byte) WriteRequest {
		return WriteRequest{
			ID: id, Status: status, Content: content, CreatedAt: createdAt, UpdatedAt: w.now().UTC(),
		}
	}

	if request.Edit {
		draft, err := artifacts.Write(writeRequest(StatusDraft, content))
		if err != nil {
			return Result{}, err
		}
		result.DraftPath = draft.Path
		if err := w.edit(draft.Path, request.Config); err != nil {
			return result, fmt.Errorf("handoff: editing draft %s: %w", draft.Path, err)
		}
		content, err = readEditedDraft(draft.Path)
		if err != nil {
			return result, err
		}
		if len(strings.TrimSpace(string(content))) == 0 {
			return result, errors.New("handoff: edited draft is empty")
		}
	}

	final, err := artifacts.Write(writeRequest(StatusFinal, content))
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
	var links conversation.ThreadStore
	if w.threads != nil {
		links, err = w.threads()
		if err != nil {
			return result, err
		}
		if _, err = links.Links(); err != nil {
			return result, err
		}
	}
	prompt := launchPrompt(final.Path)
	launched, err := w.launch(runtime, launchSession, target, request.Config, prompt)
	result.RuntimeSessionID = string(launched.RuntimeSessionID)
	if err != nil {
		return result, &PartialLaunchError{Err: err}
	}
	result.Launched = true
	if w.threads != nil {
		key := result.RuntimeSessionID
		if key == "" {
			return result, &RelationshipError{Err: errors.New("receiving launch has no identity")}
		}
		source, _ := conversation.New(request.Source.Harness, request.Source.ID)
		if err := links.Record(conversation.Link{ID: id, Source: source, TargetKey: key, Kind: "handoff", ArtifactPath: final.Path, CreatedAt: createdAt}); err != nil {
			return result, &RelationshipError{Err: err}
		}
	}
	return result, nil
}

func readEditedDraft(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("handoff: inspecting edited draft %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("handoff: edited draft %s is not a regular file", path)
	}
	if err := os.Chmod(path, artifactFileMode); err != nil {
		return nil, fmt.Errorf("handoff: restricting edited draft %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("handoff: reading edited draft %s: %w", path, err)
	}
	return content, nil
}

func projectLabel(source transcript.SessionMeta) string {
	if project := strings.TrimSpace(source.Project); project != "" {
		return project
	}
	if cwd := strings.TrimSpace(source.CWD); cwd != "" {
		base := strings.TrimSpace(filepath.Base(filepath.Clean(cwd)))
		if base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return strings.TrimSpace(source.ID)
}

// targetSession derives the target's launch template. A configured project
// for the target profile and source directory carries its args and
// environment forward when its title matches the source, or when it is the
// only such project; several untitled candidates are ambiguous, and picking
// one would launch with whichever came first in the file.
func targetSession(cfg *config.Config, source transcript.SessionMeta, target config.Account) config.Session {
	projects := cfg.Projects
	if len(projects) == 0 {
		projects = cfg.Sessions
	}
	var candidates []config.Session
	for i := range projects {
		project := projects[i]
		if !strings.EqualFold(project.EffectiveProfile(), target.Name) || !sameDirectory(project.Directory, source.CWD) {
			continue
		}
		if strings.EqualFold(project.Title, source.Project) {
			project.Directory, project.Profile, project.Account = source.CWD, target.Name, target.Name
			return project
		}
		candidates = append(candidates, project)
	}
	if len(candidates) == 1 {
		project := candidates[0]
		project.Directory, project.Profile, project.Account = source.CWD, target.Name, target.Name
		if strings.TrimSpace(source.Project) != "" {
			project.Title = source.Project
		}
		return project
	}
	title := strings.TrimSpace(source.Project)
	if title == "" {
		title = projectLabel(source)
	}
	return config.Session{Title: title, Directory: source.CWD, Profile: target.Name, Account: target.Name}
}

// sameDirectory compares a configured project directory with a transcript's
// working directory. Harnesses record the physical directory they ran in, so
// a project configured through a symlink must be resolved the same way
// (projectresolve.AbsPath) or it never matches.
func sameDirectory(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	left, right = projectresolve.AbsPath(left), projectresolve.AbsPath(right)
	return left != "" && left == right
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

func launchFresh(rt session.Runtime, sess config.Session, acc config.Account, cfg *config.Config, prompt string) (session.LaunchResult, error) {
	return session.LaunchFreshOn(rt, sess, acc, cfg, prompt)
}

// RelationshipError reports a running target whose source link could not be saved.
type RelationshipError struct{ Err error }

func (e *RelationshipError) Error() string {
	return "target launched, but its conversation link could not be saved: " + e.Err.Error()
}
func (e *RelationshipError) Unwrap() error { return e.Err }
