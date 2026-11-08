//go:build tuitest

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/worktree"
)

func fixtureWorktreeActionCmd(record func(uitestTerminalAction), entries *[]actions.WorktreeView, id actions.ID, request actions.Request) tea.Cmd {
	return func() tea.Msg {
		entry := request.Worktree
		if entry == nil {
			return worktreeActionDoneMsg{action: id, err: &actions.Error{Code: "not_found", Action: id, Message: "fixture worktree target is missing"}}
		}
		done := func(result actions.Result, err error) tea.Msg {
			result.Action = id
			return worktreeActionDoneMsg{action: id, result: result, err: err}
		}
		switch id {
		case actions.WorktreeReopen:
			switch entry.ID {
			case "wt-live":
				record(uitestTerminalAction{Kind: "focus", Target: "71"})
				return done(actions.Result{Changed: true, Message: "focused live worktree session"}, nil)
			case "wt-multi":
				if candidate := request.Parameters["session"]; candidate != "" {
					record(uitestTerminalAction{Kind: "focus", Target: candidate})
					return done(actions.Result{Changed: true, Message: "focused selected live worktree session"}, nil)
				}
				return done(actions.Result{}, &actions.Error{Code: "ambiguous_live_sessions", Action: id, Message: "multiple live sessions are inside this worktree", Candidates: []actions.Candidate{{ID: "81", DisplayName: "multi-project (work)"}, {ID: "82", DisplayName: "multi-project (personal)"}}})
			case "wt-resume":
				record(uitestTerminalAction{Kind: "launch", Title: "resume-project"})
				return done(actions.Result{Changed: true, Message: "resumed newest worktree conversation", Data: map[string]any{"continuation": "resumed"}}, nil)
			case "wt-fresh":
				record(uitestTerminalAction{Kind: "launch", Title: "fresh-project"})
				return done(actions.Result{Changed: true, Message: "launched fresh worktree session", Data: map[string]any{"continuation": "fresh"}}, nil)
			case "wt-profile":
				if request.Parameters["profile"] == "" {
					return done(actions.Result{}, &actions.Error{Code: "profile_required", Action: id, Message: "the recorded worktree profile is no longer configured", MissingParameters: []actions.ParameterSchema{{Name: "profile", Type: "string", Required: true}}})
				}
				record(uitestTerminalAction{Kind: "launch", Title: "profile-project"})
				return done(actions.Result{Changed: true, Message: "launched fresh worktree session with replacement profile"}, nil)
			}
			return done(actions.Result{Changed: true, Message: "launched fresh worktree session"}, nil)
		case actions.WorktreeRemove:
			mutation := worktree.MutationResult{Entry: *entry, CheckoutRemoved: true, MetadataForgotten: true}
			if request.Parameters["delete_branch"] == "true" {
				mutation.BranchDeleted = true
			}
			if entry.ID == "wt-partial" {
				mutation.MetadataForgotten = false
				for index := range *entries {
					if (*entries)[index].ID == entry.ID {
						(*entries)[index].Exists = false
						(*entries)[index].Classification = worktree.ClassificationMissing
						(*entries)[index].Actionable = false
					}
				}
				result := actions.Result{Changed: true, Message: "removed managed worktree partially", Data: map[string]any{"mutation": mutation}}
				return done(result, &actions.PartialResultError{ActionError: &actions.Error{Code: "metadata_invalid", Action: id, Message: "metadata removal failed"}})
			}
			removeFixtureWorktree(entries, entry.ID)
			return done(actions.Result{Changed: true, Message: "removed managed worktree", Data: map[string]any{"mutation": mutation}}, nil)
		case actions.WorktreeDiff:
			// The real action resolves the selected ID again before checking
			// availability. Model that refresh instead of trusting the row the
			// scenario passed from its last inventory snapshot.
			var current *worktree.Entry
			for _, candidate := range *entries {
				if candidate.ID == entry.ID {
					current = &candidate.Entry
					break
				}
			}
			if current == nil {
				return done(actions.Result{}, &actions.Error{Code: "not_found", Action: id, Message: "worktree was not found"})
			}
			if !current.Exists {
				return done(actions.Result{}, &actions.Error{Code: "unavailable", Action: id, Message: "worktree checkout is missing"})
			}
			if current.Classification != worktree.ClassificationValid || strings.TrimSpace(current.SourceBranch) == "" {
				return done(actions.Result{}, &actions.Error{Code: "unavailable", Action: id, Message: "no source branch was recorded for this worktree"})
			}
			if !current.GitAvailable {
				return done(actions.Result{}, &actions.Error{Code: "unavailable", Action: id, Message: "worktree Git facts are unavailable"})
			}
			entry = current
			review := worktree.Review{
				ID: entry.ID, Branch: entry.Branch, SourceBranch: entry.SourceBranch, Ahead: 2,
				Files: []worktree.FileChange{{Path: "main.go", Added: 4, Deleted: 1}}, Added: 4, Deleted: 1,
				Lands: worktree.LandsFastForward, Patch: "diff --git a/main.go b/main.go\n",
			}
			return done(actions.Result{Message: "reviewed managed worktree", Data: map[string]any{"review": review}}, nil)
		case actions.WorktreeLand:
			landed, none := true, 0
			for index := range *entries {
				if (*entries)[index].ID == entry.ID {
					(*entries)[index].ReachableFromSource, (*entries)[index].Ahead = &landed, &none
				}
			}
			return done(actions.Result{Changed: true, Message: "landed worktree on " + entry.SourceBranch, Data: map[string]any{"landing": worktree.LandResult{Entry: *entry, SourceBranch: entry.SourceBranch, Commits: 2, Landed: true}}}, nil)
		case actions.WorktreeForget:
			mutation := worktree.MutationResult{Entry: *entry, MetadataForgotten: true}
			removeFixtureWorktree(entries, entry.ID)
			return done(actions.Result{Changed: true, Message: "forgot worktree metadata", Data: map[string]any{"mutation": mutation}}, nil)
		default:
			return done(actions.Result{Changed: true, Message: "fixture worktree action completed"}, nil)
		}
	}
}

func removeFixtureWorktree(entries *[]actions.WorktreeView, id string) {
	kept := (*entries)[:0]
	for _, entry := range *entries {
		if entry.ID != id {
			kept = append(kept, entry)
		}
	}
	*entries = kept
}
