package actions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

func lifecycleManager(deps Dependencies) WorktreeLifecycle {
	if deps.WorktreeManager != nil {
		return deps.WorktreeManager
	}
	return worktree.NewManager()
}

func worktreeOpenAvailability(deps Dependencies, request Request, requireProject bool) Availability {
	if request.Worktree == nil {
		return Availability{Available: true}
	}
	entry := *request.Worktree
	if !entry.Exists {
		return Availability{Reason: "worktree checkout is missing"}
	}
	if requireProject {
		if _, ok := configuredWorktreeProject(deps.Config, entry); !ok {
			return Availability{Reason: "worktree does not match a configured project"}
		}
	}
	return Availability{Available: true}
}

func worktreeRemoveAvailability(request Request) Availability {
	if request.Worktree == nil {
		return Availability{Available: true}
	}
	entry := *request.Worktree
	if entry.Usage.Live || entry.Usage.Looping {
		return Availability{Reason: "worktree is active"}
	}
	if entry.Locked {
		return Availability{Reason: "worktree is locked"}
	}
	if entry.Dirty {
		return Availability{Reason: "worktree is dirty"}
	}
	if !entry.Exists {
		if parameterBool(request.Parameters, "delete_branch") && entry.Classification == worktree.ClassificationMissing {
			return Availability{Available: true}
		}
		return Availability{Reason: "worktree checkout is missing"}
	}
	if !entry.Actionable || !entry.GitAvailable || !entry.StatusAvailable {
		return Availability{Reason: "worktree Git safety facts are unavailable"}
	}
	return Availability{Available: true}
}

func worktreeForgetAvailability(request Request) Availability {
	if request.Worktree == nil {
		return Availability{Available: true}
	}
	entry := *request.Worktree
	if entry.Exists && entry.Classification != worktree.ClassificationMetadataInvalid {
		return Availability{Reason: "healthy metadata for a present checkout cannot be forgotten"}
	}
	return Availability{Available: true}
}

func listWorktrees(ctx context.Context, deps Dependencies) (Result, error) {
	entries, inventoryWarning := lifecycleManager(deps).List(ctx)
	if inventoryWarning != nil && len(entries) == 0 {
		return Result{}, &Error{Code: "git_unavailable", Action: WorktreeList, Message: inventoryWarning.Error()}
	}
	snapshot := probeWorktreeUsage(deps, entries)
	if inventoryWarning != nil {
		snapshot.warnings = append(snapshot.warnings, "inventory: "+inventoryWarning.Error())
	}
	for index := range entries {
		key := entryKey(entries[index])
		entries[index].Usage.Live = len(snapshot.live[key]) > 0
		entries[index].Usage.Looping = len(snapshot.loops[key]) > 0
	}
	worktree.SortEntries(entries)
	views := make([]WorktreeView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, worktreeView(entry, snapshot))
	}
	return Result{Message: fmt.Sprintf("listed %d managed worktree(s)", len(views)), Data: map[string]any{
		"worktrees": views, "warnings": sortedStrings(snapshot.warnings),
	}}, nil
}

func showWorktree(ctx context.Context, deps Dependencies, request Request) (Result, error) {
	entry, err := resolveWorktree(ctx, deps, request, WorktreeShow)
	if err != nil {
		return Result{}, err
	}
	snapshot := probeWorktreeUsage(deps, []worktree.Entry{entry})
	return Result{Message: "showing managed worktree", Data: map[string]any{
		"worktree": worktreeView(entry, snapshot), "warnings": sortedStrings(snapshot.warnings),
	}}, nil
}

func reopenWorktree(ctx context.Context, deps Dependencies, request Request) (Result, error) {
	entry, err := resolveWorktree(ctx, deps, request, WorktreeReopen)
	if err != nil {
		return Result{}, err
	}
	if !entry.Exists {
		return Result{}, &Error{Code: "metadata_invalid", Action: WorktreeReopen, Message: "worktree checkout is missing"}
	}
	if deps.Kitty == nil || deps.Config == nil {
		return Result{}, dependencyError(WorktreeReopen, "Kitty client and configuration")
	}
	project, ok := configuredWorktreeProject(deps.Config, entry)
	if !ok {
		return Result{}, &Error{Code: "not_found", Action: WorktreeReopen, Message: "worktree does not match a configured project"}
	}

	liveProbe := deps.LiveSessions
	if liveProbe == nil {
		liveProbe = func(cfg *config.Config) ([]session.RunningSession, error) {
			running, _, err := session.ListWithConfig(deps.Kitty, cfg)
			return running, err
		}
	}
	running, err := liveProbe(deps.Config)
	if err != nil {
		return Result{}, &Error{Code: "probe_unavailable", Action: WorktreeReopen, Message: fmt.Sprintf("checking live sessions: %v", err)}
	}
	matches := make([]session.RunningSession, 0)
	for _, candidate := range running {
		if pathAtOrBelow(entry.Path, candidate.Directory) {
			matches = append(matches, candidate)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].TabID < matches[j].TabID })
	requestedSession := strings.TrimSpace(request.Parameters["session"])
	if requestedSession != "" {
		for _, candidate := range matches {
			if liveSessionView(candidate).ID == requestedSession {
				return focusWorktreeSession(deps.Kitty, candidate)
			}
		}
		return Result{}, &Error{Code: "not_found", Action: WorktreeReopen, Message: fmt.Sprintf("live-session candidate %q was not found", requestedSession), Candidates: liveSessionCandidates(matches)}
	}
	if len(matches) == 1 {
		return focusWorktreeSession(deps.Kitty, matches[0])
	}
	if len(matches) > 1 {
		return Result{}, &Error{Code: "ambiguous_live_sessions", Action: WorktreeReopen, Message: "multiple live sessions are inside this worktree", Candidates: liveSessionCandidates(matches)}
	}

	indexLoader := deps.TranscriptIndex
	if indexLoader == nil {
		indexLoader = transcript.LoadIndex
	}
	idx, loadWarning := indexLoader()
	if idx == nil {
		if loadWarning == nil {
			loadWarning = errors.New("transcript index loader returned no index")
		}
		return Result{}, &Error{Code: "probe_unavailable", Action: WorktreeReopen, Message: loadWarning.Error()}
	}
	refreshWarning := idx.Refresh(deps.Config, deps.Readers)
	warnings := warningStrings(loadWarning, refreshWarning)
	conversations := make([]transcript.SessionMeta, 0)
	for _, meta := range idx.List(transcript.ListFilter{}) {
		if pathAtOrBelow(entry.Path, meta.CWD) {
			conversations = append(conversations, meta)
		}
	}
	sort.Slice(conversations, func(i, j int) bool { return newerTranscript(conversations[i], conversations[j]) })
	if len(conversations) > 0 {
		return resumeWorktreeConversation(deps, entry, project, conversations[0], warnings)
	}
	return launchFreshWorktree(deps, entry, project, strings.TrimSpace(request.Parameters["profile"]), warnings)
}

func focusWorktreeSession(client kitty.KittyClient, running session.RunningSession) (Result, error) {
	if err := client.FocusTab(kitty.MatchTabByID(running.TabID)); err != nil {
		return Result{}, err
	}
	return Result{Changed: true, Message: "focused live worktree session", Data: map[string]any{
		"continuation": "focused", "session": liveSessionView(running),
	}}, nil
}

func resumeWorktreeConversation(deps Dependencies, entry worktree.Entry, project config.Session, meta transcript.SessionMeta, warnings []string) (Result, error) {
	identity, err := conversationIdentity(meta, WorktreeReopen)
	if err != nil {
		return Result{}, err
	}
	if err := validateIndexedSource(meta, identity, deps.Readers); err != nil {
		return Result{}, &Error{Code: "probe_unavailable", Action: WorktreeReopen, Message: err.Error()}
	}
	profile := strings.TrimSpace(meta.DisplayProfile())
	account, ok := accountByName(deps.Config, profile)
	if !ok || !strings.EqualFold(account.EffectiveHarness(), identity.Harness) {
		return Result{}, profileRequiredError("the indexed conversation's profile is no longer usable")
	}
	project.Directory = entry.Path
	project.Profile, project.Account = account.Name, account.Name
	if err := session.LaunchContinuity(deps.Kitty, project, account, deps.Config, harness.InvocationResume, identity.SessionID, ""); err != nil {
		return Result{}, &Error{Code: "launch_failed", Action: WorktreeReopen, Message: err.Error()}
	}
	return Result{Changed: true, Message: "resumed newest worktree conversation", Data: map[string]any{
		"continuation": "resumed", "harness": identity.Harness, "session_id": identity.SessionID,
		"warnings": warnings,
	}}, nil
}

func launchFreshWorktree(deps Dependencies, entry worktree.Entry, project config.Session, replacement string, warnings []string) (Result, error) {
	explicitReplacement := replacement != ""
	profile := replacement
	if profile == "" {
		profile = strings.TrimSpace(entry.Profile)
	}
	if profile == "" {
		profile = project.EffectiveProfile()
	}
	account, ok := accountByName(deps.Config, profile)
	if !ok {
		return Result{}, profileRequiredError("the recorded worktree profile is no longer configured")
	}
	if !explicitReplacement && entry.Harness != "" && !strings.EqualFold(account.EffectiveHarness(), entry.Harness) {
		return Result{}, profileRequiredError(fmt.Sprintf("profile %q now uses %q instead of %q", account.Name, account.EffectiveHarness(), entry.Harness))
	}
	project.Directory = entry.Path
	project.Profile, project.Account = account.Name, account.Name
	if err := session.Launch(deps.Kitty, project, account, deps.Config, session.LaunchPolicyAlwaysNewInstance); err != nil {
		return Result{}, &Error{Code: "launch_failed", Action: WorktreeReopen, Message: err.Error()}
	}
	return Result{Changed: true, Message: "launched fresh worktree session", Data: map[string]any{
		"continuation": "fresh", "profile": account.Name, "harness": account.EffectiveHarness(), "warnings": warnings,
	}}, nil
}

func profileRequiredError(message string) error {
	return &Error{Code: "profile_required", Action: WorktreeReopen, Message: message, MissingParameters: []ParameterSchema{{Name: "profile", Type: "string", Description: "replacement configured profile", Required: true}}}
}

func openWorktree(ctx context.Context, deps Dependencies, request Request, editor bool) (Result, error) {
	action := WorktreeShell
	if editor {
		action = WorktreeEditor
	}
	entry, err := resolveWorktree(ctx, deps, request, action)
	if err != nil {
		return Result{}, err
	}
	if !entry.Exists {
		return Result{}, &Error{Code: "metadata_invalid", Action: action, Message: "worktree checkout is missing"}
	}
	if deps.Kitty == nil {
		return Result{}, dependencyError(action, "Kitty client")
	}
	project, matched := configuredWorktreeProject(deps.Config, entry)
	opened := false
	if matched {
		profile := strings.TrimSpace(entry.Profile)
		if profile == "" {
			profile = project.EffectiveProfile()
		}
		if account, ok := accountByName(deps.Config, profile); ok {
			project.Directory = entry.Path
			project.Profile, project.Account = account.Name, account.Name
			if editor {
				err = session.OpenInEditor(deps.Kitty, project, account, deps.Config)
			} else {
				err = session.OpenDirectory(deps.Kitty, project, account, deps.Config)
			}
			opened = true
		}
	}
	if !opened {
		title := worktreeTitle(entry)
		if editor {
			if deps.Config == nil {
				return Result{}, dependencyError(action, "configuration")
			}
			err = session.OpenPathInEditor(deps.Kitty, entry.Path, title, deps.Config)
		} else {
			err = deps.Kitty.LaunchTab(kitty.LaunchOpts{Title: title, Directory: entry.Path})
		}
	}
	if err != nil {
		return Result{}, &Error{Code: "launch_failed", Action: action, Message: err.Error()}
	}
	message := "opened worktree shell"
	if editor {
		message = "opened worktree editor"
	}
	return Result{Changed: true, Message: message, Data: map[string]any{"path": entry.Path}}, nil
}

func removeWorktree(ctx context.Context, deps Dependencies, request Request) (Result, error) {
	entry, err := resolveWorktree(ctx, deps, request, WorktreeRemove)
	if err != nil {
		return Result{}, err
	}
	snapshot := probeWorktreeUsage(deps, []worktree.Entry{entry})
	if len(snapshot.warnings) > 0 {
		return Result{}, &Error{Code: "probe_unavailable", Action: WorktreeRemove, Message: strings.Join(sortedStrings(snapshot.warnings), "; ")}
	}
	key := entryKey(entry)
	if len(snapshot.live[key]) > 0 || len(snapshot.loops[key]) > 0 {
		candidates := append(liveSessionCandidates(snapshot.live[key]), loopCandidates(snapshot.loops[key])...)
		return Result{}, &Error{Code: "worktree_active", Action: WorktreeRemove, Message: "worktree has a live session or active loop", Candidates: candidates}
	}
	selector := mutationSelector(request, entry)
	mutation, err := lifecycleManager(deps).Remove(ctx, selector, worktree.RemoveOptions{DeleteBranch: parameterBool(request.Parameters, "delete_branch")})
	result := mutationActionResult(WorktreeRemove, "removed managed worktree", mutation)
	if err != nil {
		var partial *worktree.PartialMutationError
		if errors.As(err, &partial) {
			mapped := mapLifecycleError(WorktreeRemove, err)
			return result, &PartialResultError{ActionError: mapped}
		}
		return Result{}, mapLifecycleError(WorktreeRemove, err)
	}
	return result, nil
}

func forgetWorktree(ctx context.Context, deps Dependencies, request Request) (Result, error) {
	entry, err := resolveWorktree(ctx, deps, request, WorktreeForget)
	if err != nil {
		return Result{}, err
	}
	mutation, err := lifecycleManager(deps).Forget(ctx, mutationSelector(request, entry))
	result := mutationActionResult(WorktreeForget, "forgot worktree metadata", mutation)
	if err != nil {
		return Result{}, mapLifecycleError(WorktreeForget, err)
	}
	return result, nil
}

func resolveWorktree(ctx context.Context, deps Dependencies, request Request, action ID) (worktree.Entry, error) {
	if request.Worktree != nil {
		return *request.Worktree, nil
	}
	entry, err := lifecycleManager(deps).Resolve(ctx, request.Selector)
	if err != nil {
		return worktree.Entry{}, mapLifecycleError(action, err)
	}
	return entry, nil
}

func mapLifecycleError(action ID, err error) *Error {
	var selectorErr *worktree.SelectorError
	if errors.As(err, &selectorErr) {
		code := "not_found"
		if errors.Is(err, worktree.ErrSelectorAmbiguous) {
			code = "ambiguous_selector"
		}
		candidates := make([]Candidate, 0, len(selectorErr.Candidates))
		for _, entry := range selectorErr.Candidates {
			candidates = append(candidates, Candidate{ID: entry.ID, DisplayName: worktreeTitle(entry), CanonicalPath: entry.Path})
		}
		return &Error{Code: code, Action: action, Message: err.Error(), Candidates: candidates, Cause: err}
	}
	code := "git_unavailable"
	switch {
	case errors.Is(err, worktree.ErrWorktreeDirty):
		code = "worktree_dirty"
	case errors.Is(err, worktree.ErrWorktreeLocked):
		code = "worktree_locked"
	case errors.Is(err, worktree.ErrBranchUnmerged), errors.Is(err, worktree.ErrSourceBranchMissing):
		code = "branch_unmerged"
	case errors.Is(err, worktree.ErrMetadataInvalid), errors.Is(err, worktree.ErrMetadataMissing), errors.Is(err, worktree.ErrForgetRefused), errors.Is(err, worktree.ErrWorktreeMissing):
		code = "metadata_invalid"
	case errors.Is(err, worktree.ErrGitUnavailable), errors.Is(err, worktree.ErrBranchMissing):
		code = "git_unavailable"
	}
	return &Error{Code: code, Action: action, Message: err.Error()}
}

func configuredWorktreeProject(cfg *config.Config, entry worktree.Entry) (config.Session, bool) {
	if cfg == nil {
		return config.Session{}, false
	}
	projects := cfg.Projects
	if len(projects) == 0 {
		projects = cfg.Sessions
	}
	type scored struct {
		project config.Session
		score   int
	}
	var matches []scored
	for _, project := range projects {
		directory := canonicalComparablePath(config.ExpandTilde(project.Directory))
		score := 0
		if entry.SourceRoot != "" && directory == canonicalComparablePath(entry.SourceRoot) {
			score += 2
		}
		if entry.ProjectLabel != "" && project.Title == entry.ProjectLabel {
			score++
		}
		if score > 0 {
			matches = append(matches, scored{project: project, score: score})
		}
	}
	if len(matches) == 0 {
		return config.Session{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if len(matches) > 1 && matches[0].score == matches[1].score {
		return config.Session{}, false
	}
	return matches[0].project, true
}

func newerTranscript(left, right transcript.SessionMeta) bool {
	if !left.LastAt.Equal(right.LastAt) {
		return left.LastAt.After(right.LastAt)
	}
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.After(right.StartedAt)
	}
	if left.Harness != right.Harness {
		return left.Harness < right.Harness
	}
	return left.ID < right.ID
}

func mutationSelector(request Request, entry worktree.Entry) string {
	if selector := strings.TrimSpace(request.Selector); selector != "" {
		return selector
	}
	return entry.ID
}

func mutationActionResult(action ID, message string, mutation worktree.MutationResult) Result {
	return Result{Action: action, Changed: mutation.CheckoutRemoved || mutation.BranchDeleted || mutation.MetadataForgotten, Message: message, Data: map[string]any{"mutation": mutation}}
}

func worktreeTitle(entry worktree.Entry) string {
	if entry.ProjectLabel != "" {
		return entry.ProjectLabel + " worktree"
	}
	if entry.Branch != "" {
		return entry.Branch
	}
	return entry.ID
}

func warningStrings(values ...error) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, value.Error())
		}
	}
	return sortedStrings(out)
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
