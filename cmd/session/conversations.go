package session

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

// Dependency seams keep the filesystem-backed commands testable without a
// real user config, Kitty instance, or daemon.
var (
	sessionCommandsLoadConfig       = cli.LoadRunnableConfig
	sessionCommandsLoadSessionIndex = transcript.LoadIndexAt
	sessionCommandsSessionIndexPath = transcript.IndexPath
	sessionCommandsSessionReaders   = cli.TranscriptReaders
	sessionCommandsNewKitty         = func() kitty.KittyClient { return cli.NewKittyClient() }
	sessionCommandsDaemon           = func(req protocol.Request) (protocol.Response, error) { return ipc.NewClient().Send(req) }
	sessionCommandsNewHandoff       = func() handoff.Runner { return handoff.NewWorkflow() }
	sessionCommandsNewWorktree      = func() worktree.Runner { return worktree.NewWorkflow() }
	sessionCommandsNow              = time.Now
	sessionListIsTerminal           = cli.IsTerminal
	sessionListTerminalWidth        = cli.TerminalWidth
	sessionListGetwd                = os.Getwd
)

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List indexed conversation sessions",
	Long: `Lists the conversations in the transcript index, most recently active first:
the interactive ones Past Sessions shows, unless --all adds automated and
unclassified ones. The selector flags narrow the list (--harness, --profile,
--project, --since, --here, --live, --conversation), and --limit and
--offset page through it.

In a terminal the list is a table of the 25 most recent sessions unless
--limit is given, with each session ID cut to the shortest prefix that still
selects it. Piped, every matching session is one headerless tab-separated
line with full IDs and timestamps. With --json it reports {total, offset,
has_more, hidden_automated, sessions}.`,
	Example: `  amx session list
  amx session list --here --since 2d
  amx session list --project api --harness claude -n 10
  amx session list --live --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.ConfigFailure(cmd, jsonOutput, err)
		}
		selection, code, err := readSessionSelection(cmd, cfg)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, code, err)
		}
		idx, err := loadRefreshedSessionIndex(cfg, sessionCommandsSessionReaders())
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		if err := selection.resolveProjects(cfg, idx); err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", err)
		}
		allSelection := selection
		allSelection.limit, allSelection.offset = 0, 0
		total := len(allSelection.narrow(idx.List(selection.filter)))
		metas := selection.narrow(idx.List(selection.filter))
		out := cmd.OutOrStdout()
		terminal := !jsonOutput && sessionListIsTerminal(out)
		omitted := 0
		if terminal {
			var keep int
			keep, omitted = tableDefaultLimit(cmd, len(metas))
			metas = metas[:keep]
		}
		views := sessionViews(metas)
		// Asked even when piped, since piped rows carry a LIVE field too.
		live, liveKnown := selection.liveConversations(true)
		markLive(views, live, sessionCommandsCurrentWindow())
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionListData{Sessions: views, Total: total, Offset: selection.offset, HasMore: selection.offset+len(views) < total, HiddenAutomated: hiddenAutomated(selection, idx)})
		}
		if terminal {
			if len(views) == 0 {
				if total > 0 {
					_, err := fmt.Fprintln(out, offsetPastResultsMessage(selection.offset, total))
					return err
				}
				_, err := fmt.Fprintln(out, noSessionsMessage(cmd, selection, idx))
				return err
			}
			if err := writeSessionTable(out, views, abbreviateSessionIDs(idx.List(transcript.ListFilter{})), sessionCommandsNow(), sessionListTerminalWidth(out), liveKnown); err != nil {
				return err
			}
			return writeOmittedFooter(out, len(views), omitted, hiddenAutomated(selection, idx))
		}
		writeSessionLines(out, views, liveKnown)
		return nil
	},
}

// formatAgo renders how long ago at was, coarsely: the list is for picking a
// conversation, and "3h ago" answers that faster than a timestamp does.
func formatAgo(at, now time.Time) string {
	if at.IsZero() {
		return "-"
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

var sessionContinueCmd = newConversationActionCommand("continue", actions.Continue, "Focus an exact live conversation or resume its indexed source",
	`Picks one conversation back up. One that is open right now is focused; a
closed one is resumed in a new session with its harness's own resume. A
conversation another running session already holds is never resumed twice.
A selector is one of `+conversationSelectorHelp+`.

--here continues the most recently active conversation started in the
current directory or below it, and says which one it picked.

With --json it reports {action, changed, message, data}.`,
	`  amx session continue 3c1e9f0a
  amx session continue codex 3c1e9f0a-7b2d-4e8f-9a1b-2c3d4e5f6a7b
  amx session continue --here
  amx session continue 3c1e9f0a --backend tmux --json`)

var sessionForkCmd = newConversationActionCommand("fork", actions.Fork, "Fork an indexed conversation (portable for Copilot)",
	`Starts a new conversation that carries on from an indexed one, leaving the
original untouched. Claude and Codex fork natively; Copilot, which has no
native fork, is forked portably from a handoff of the conversation. A
selector is one of `+conversationSelectorHelp+`.

--here forks the most recently active conversation started in the current
directory or below it. To fork into an isolated Git worktree instead, use
`+"`amx session fork-worktree`"+`.

With --json it reports {action, changed, message, data}.`,
	`  amx session fork 3c1e9f0a
  amx session fork --here --backend tmux`)

var sessionForkWorktreeCmd = &cobra.Command{
	Use:               "fork-worktree <harness> <session-id> | <harness>:<session-id> | <session-id>",
	ValidArgsFunction: completeConversation,
	Short:             "Fork an indexed conversation in a new managed Git worktree",
	Long: `Creates a managed Git worktree from the repository the conversation ran in,
on a new branch, and forks the conversation into it, so the fork's changes
stay apart from the original checkout. It asks for --yes. A dirty source is
refused unless --allow-dirty is given, and even then its uncommitted changes
are not copied. A selector is one of ` + conversationSelectorHelp + `.

The worktree outlives the session; ` + "`amx worktree`" + ` lists, reviews, lands, and
removes it. With --json it reports {action, changed, message, data}.`,
	Example: `  amx session fork-worktree 3c1e9f0a --yes
  amx session fork-worktree claude:3c1e9f0a-7b2d-4e8f-9a1b-2c3d4e5f6a7b --yes --allow-dirty`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		allowDirty, _ := cmd.Flags().GetBool("allow-dirty")
		confirmed, _ := cmd.Flags().GetBool("yes")
		identity, err := conversationIdentityFromArgs(cmd, jsonOutput, "fork-worktree", args)
		if err != nil {
			return err
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.ConfigFailure(cmd, jsonOutput, err)
		}
		meta, err := resolveConversationOrFail(cmd, jsonOutput, cfg, sessionCommandsSessionReaders(), identity)
		if err != nil {
			return err
		}
		parameters := map[string]string{}
		if allowDirty {
			parameters["allow_dirty"] = "true"
		}
		return executeSessionAction(cmd, jsonOutput, cfg, actions.ForkWorktree, actions.Request{Conversation: meta, Parameters: parameters, Confirmed: confirmed})
	},
}

var sessionHandoffCmd = &cobra.Command{
	Use:               "handoff <harness> <session-id> | <harness>:<session-id> | <session-id>",
	ValidArgsFunction: completeConversation,
	Short:             "Launch a fresh target profile from a portable handoff pack",
	Long: `Writes a handoff pack for a conversation: a bounded Markdown summary of where
it stands and its recent messages, and launches a new agent under --profile
(of any harness) that starts from it. --edit opens the pack in your editor
before launch; --no-launch writes it and stops. The pack is kept either way.
A selector is ` + "`<harness>:<session-id>`" + ` or a bare session ID or unique prefix.

With --json it reports {action, changed, message, data}, including the
pack's path.`,
	Example: `  amx session handoff 3c1e9f0a --profile codex-work
  amx session handoff claude:3c1e9f0a-7b2d-4e8f-9a1b-2c3d4e5f6a7b --profile copilot --edit
  amx session handoff 3c1e9f0a --profile codex-work --no-launch --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		edit, _ := cmd.Flags().GetBool("edit")
		noLaunch, _ := cmd.Flags().GetBool("no-launch")
		profile, _ := cmd.Flags().GetString("profile")
		if edit && jsonOutput {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_options", errors.New("--edit cannot be used with --json"))
		}
		// The same selector forms as continue, fork, and show, read by the
		// same parser, so one copied from any of them works here.
		identity, err := conversationIdentityFromArgs(cmd, jsonOutput, "handoff", args)
		if err != nil {
			return err
		}
		if strings.TrimSpace(profile) == "" {
			return cli.MissingParameter(cmd, jsonOutput, "profile", "target configured profile")
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.ConfigFailure(cmd, jsonOutput, err)
		}
		meta, err := resolveConversationOrFail(cmd, jsonOutput, cfg, sessionCommandsSessionReaders(), identity)
		if err != nil {
			return err
		}
		parameters := map[string]string{"profile": profile}
		if edit {
			parameters["edit"] = "true"
		}
		if noLaunch {
			parameters["no_launch"] = "true"
		}
		return executeSessionAction(cmd, jsonOutput, cfg, actions.Handoff, actions.Request{Conversation: meta, Parameters: parameters})
	},
}

type sessionListData struct {
	Total           int           `json:"total"`
	Offset          int           `json:"offset"`
	HasMore         bool          `json:"has_more"`
	HiddenAutomated int           `json:"hidden_automated"`
	Sessions        []sessionView `json:"sessions"`
}

type sessionView struct {
	ID           string    `json:"id"`
	Harness      string    `json:"harness"`
	Profile      string    `json:"profile"`
	Profiles     []string  `json:"profiles"`
	Project      string    `json:"project"`
	Projects     []string  `json:"projects"`
	CWD          string    `json:"cwd"`
	StartedAt    time.Time `json:"started_at"`
	LastAt       time.Time `json:"last_at"`
	Path         string    `json:"path"`
	FileSize     int64     `json:"file_size"`
	FileModTime  time.Time `json:"file_mod_time"`
	MessageCount int       `json:"message_count"`
	Snippet      string    `json:"snippet"`
	Title        string    `json:"title,omitempty"`
	// Ending is the opening of the agent's final prose reply; see
	// transcript.SessionMeta.Ending.
	Ending string `json:"ending,omitempty"`
	// Live is the Kitty tab or tmux session the conversation is open in, when the daemon and
	// Kitty could both be asked and agreed on exactly one.
	Live *liveConversation `json:"live,omitempty"`
}

func sessionViews(items []transcript.SessionMeta) []sessionView {
	out := make([]sessionView, 0, len(items))
	for _, item := range items {
		out = append(out, sessionView{ID: item.ID, Harness: item.Harness, Profile: item.DisplayProfile(), Profiles: append([]string{}, item.ProfileNames()...), Project: item.Project, Projects: append([]string{}, item.ProjectNames()...), CWD: item.CWD, StartedAt: item.StartedAt, LastAt: item.LastAt, Path: item.Path, FileSize: item.FileSize, FileModTime: item.FileModTime, MessageCount: item.MessageCount, Snippet: item.Snippet, Title: item.Title, Ending: item.Ending})
	}
	return out
}
