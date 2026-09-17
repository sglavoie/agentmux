package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// Consequence strings shown on line 2 of the confirm overlay so the user
// understands what pressing `y` will actually do. The transcript path is
// omitted because RunningSession does not currently expose it.
//
// The close and restart sentences are not here. They belong to the actions that
// raise them, which both surfaces now reach through the shared controller, so
// they are declared once in tui/sessionactions beside the verbs they pair with.
// They also no longer name Claude: they had said so on a Codex or a Copilot row
// since the second harness landed, and a sentence naming the wrong program is
// worse than one naming none.
const worktreeForkConsequence = "Creates a tracked Git worktree and forks there. Dirty sources are refused; CLI --allow-dirty omits uncommitted changes."

// renderConfirm renders the confirmation overlay's interior:
//
//	Close "<title>"?
//	<consequence describing what happens on y>
//
// The footer (rendered separately) carries the y/n/esc affordances.
func renderConfirm(m Model) string {
	return sessionactions.RenderPrompt(confirmPromptInput(m))
}

// confirmVerbAndTitle resolves what is being confirmed and what it is being
// confirmed about. It is this model's half of the overlay: the worktree and ASL
// prompts read targets that have nothing to do with session actions, and they
// stay here rather than being pushed into a package that names neither.
func confirmVerbAndTitle(m Model) (verb, title string) {
	title = dashboard.SessionDisplayTitle(m.targetSession)
	if text, ok := sessionactions.ConfirmVerbText(m.confirmAction); ok {
		return text, title
	}
	switch m.confirmAction {
	case "fork worktree":
		verb = "Fork in new worktree"
	case "worktree remove":
		verb = "Remove worktree checkout"
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	case "worktree delete branch":
		verb = "Remove worktree and delete branch"
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	case "worktree forget":
		verb = "Forget worktree metadata"
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	case asl.ConfirmStop:
		verb = "Stop after the current session"
		title = aslConfirmationTitle(m.aslActionTarget)
	case asl.ConfirmAbort:
		verb = "Abort the in-flight turn"
		title = aslConfirmationTitle(m.aslActionTarget)
	default:
		verb = m.confirmAction
	}
	return verb, title
}

// confirmPromptInput is the value the shared renderer draws from. The verb and
// the subject are resolved above, from state only this model holds; everything
// after that is presentation and lives in tui/sessionactions, where the surface
// F2 opens can reach it without importing this package.
func confirmPromptInput(m Model) sessionactions.PromptInput {
	verb, title := confirmVerbAndTitle(m)
	return sessionactions.PromptInput{
		Verb:        verb,
		Subject:     title,
		Consequence: m.confirmConsequence,
		Width:       m.width,
	}
}

// aslConfirmationTitle names the captured run. It reads the capture rather than
// the current selection, so the overlay keeps describing what the operator
// reviewed even if a refresh moves the list underneath it.
func aslConfirmationTitle(target *asl.ActionTarget) string {
	if target == nil {
		return "selected run"
	}
	if strings.TrimSpace(target.Label) != "" {
		return target.Label
	}
	return target.RowID
}

func worktreeConfirmationTitle(target *actions.WorktreeView) string {
	if target == nil {
		return "selected worktree"
	}
	if strings.TrimSpace(target.ProjectLabel) != "" {
		return target.ProjectLabel + " · " + target.Branch
	}
	if strings.TrimSpace(target.Branch) != "" {
		return target.Branch
	}
	return target.ID
}

// renderConfirmBox composites the prompt as a centered modal over the dimmed
// dashboard. The width policy — a preferred 70 columns, always two terminal
// cells of margin — is the shared renderer's, so the box this model draws and
// the one another host draws cannot end up different sizes.
func renderConfirmBox(m Model) string {
	return sessionactions.RenderPromptBox(confirmPromptInput(m))
}
