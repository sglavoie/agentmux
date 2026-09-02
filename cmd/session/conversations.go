package session

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
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
)

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List indexed conversation sessions",
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
		}
		idx, err := loadRefreshedSessionIndex(cfg, sessionCommandsSessionReaders())
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		views := sessionViews(idx.List(transcript.ListFilter{}))
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionListData{Sessions: views})
		}
		for _, item := range views {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", item.Harness, item.Profile, item.Project, formatTime(item.LastAt), item.ID)
		}
		return nil
	},
}

var sessionContinueCmd = newConversationActionCommand("continue", actions.Continue, "Focus an exact live conversation or resume its indexed source")
var sessionForkCmd = newConversationActionCommand("fork", actions.Fork, "Fork an indexed conversation (portable for Copilot)")

var sessionForkWorktreeCmd = &cobra.Command{
	Use:   "fork-worktree <harness> <session-id>",
	Short: "Fork an indexed conversation in a new managed Git worktree",
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
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
		}
		meta, err := resolveIndexedConversation(cfg, sessionCommandsSessionReaders(), identity)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		if meta == nil {
			return cli.CommandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s:%s was not found", identity.Harness, identity.SessionID))
		}
		parameters := map[string]string{}
		if allowDirty {
			parameters["allow_dirty"] = "true"
		}
		return executeSessionAction(cmd, jsonOutput, cfg, actions.ForkWorktree, actions.Request{Conversation: meta, Parameters: parameters, Confirmed: confirmed})
	},
}

var sessionHandoffCmd = &cobra.Command{
	Use:   "handoff <conversation>",
	Short: "Launch a fresh target profile from a portable handoff pack",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		edit, _ := cmd.Flags().GetBool("edit")
		noLaunch, _ := cmd.Flags().GetBool("no-launch")
		profile, _ := cmd.Flags().GetString("profile")
		if edit && jsonOutput {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_options", errors.New("--edit cannot be used with --json"))
		}
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return cli.MissingParameter(cmd, jsonOutput, "conversation", "indexed conversation as <harness>:<session-id>")
		}
		if len(args) != 1 {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("session handoff accepts exactly one conversation selector"))
		}
		if strings.TrimSpace(profile) == "" {
			return cli.MissingParameter(cmd, jsonOutput, "profile", "target configured profile")
		}
		identity, ok := parseConversationSelector(args[0])
		if !ok {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_selector", errors.New("conversation must be formatted as <harness>:<session-id>"))
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
		}
		readers := sessionCommandsSessionReaders()
		meta, err := resolveIndexedConversation(cfg, readers, identity)
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		if meta == nil {
			return cli.CommandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s:%s was not found", identity.Harness, identity.SessionID))
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

func newConversationActionCommand(name string, actionID actions.ID, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <harness> <session-id>",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			identity, err := conversationIdentityFromArgs(cmd, jsonOutput, name, args)
			if err != nil {
				return err
			}
			cfg, err := sessionCommandsLoadConfig()
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
			}
			meta, err := resolveIndexedConversation(cfg, sessionCommandsSessionReaders(), identity)
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
			}
			if meta == nil {
				return cli.CommandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s:%s was not found", identity.Harness, identity.SessionID))
			}
			return executeSessionAction(cmd, jsonOutput, cfg, actionID, actions.Request{Conversation: meta})
		},
	}
}

func conversationIdentityFromArgs(cmd *cobra.Command, jsonOutput bool, name string, args []string) (conversation.Identity, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return conversation.Identity{}, cli.MissingParameter(cmd, jsonOutput, "harness", "source harness (claude, codex, or copilot)")
	}
	if len(args) == 1 || strings.TrimSpace(args[1]) == "" {
		return conversation.Identity{}, cli.MissingParameter(cmd, jsonOutput, "session-id", "indexed conversation session ID")
	}
	if len(args) != 2 {
		return conversation.Identity{}, cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("session %s accepts exactly a harness and session ID", name))
	}
	identity, ok := conversation.New(args[0], args[1])
	if !ok {
		return conversation.Identity{}, cli.CommandFailure(cmd, jsonOutput, "invalid_selector", errors.New("harness and session-id must be non-empty"))
	}
	return identity, nil
}

func loadRefreshedSessionIndex(cfg *config.Config, readers map[string]transcript.Reader) (*transcript.Index, error) {
	idx, loadWarning := sessionCommandsLoadSessionIndex(sessionCommandsSessionIndexPath())
	if idx == nil {
		if loadWarning == nil {
			loadWarning = errors.New("transcript index loader returned no index")
		}
		return nil, loadWarning
	}
	refreshWarning := idx.Refresh(cfg, readers)
	if warning := errors.Join(loadWarning, refreshWarning); warning != nil {
		// Recovering filesystem caches return usable snapshots with warning
		// errors. Keep continuity available while making degradation visible.
		slog.Warn("session index refresh completed with warnings", "path", idx.Path(), "err", warning)
	}
	return idx, nil
}

func resolveIndexedConversation(cfg *config.Config, readers map[string]transcript.Reader, identity conversation.Identity) (*transcript.SessionMeta, error) {
	idx, err := loadRefreshedSessionIndex(cfg, readers)
	if err != nil {
		return nil, err
	}
	meta, found := idx.Get(transcript.IndexKey(identity.Harness, identity.SessionID))
	if !found || meta.HiddenFromIndex {
		return nil, nil
	}
	return &meta, nil
}

func executeSessionAction(cmd *cobra.Command, jsonOutput bool, cfg *config.Config, actionID actions.ID, request actions.Request) error {
	deps := actions.Dependencies{
		Kitty: sessionCommandsNewKitty(), Config: cfg, Readers: sessionCommandsSessionReaders(), Daemon: sessionCommandsDaemon, Handoff: sessionCommandsNewHandoff(), Worktree: sessionCommandsNewWorktree(),
	}
	result, err := actions.DefaultRegistry().Execute(cli.CommandContext(cmd), deps, actionID, request)
	if err != nil {
		return sessionActionCommandError(cmd, jsonOutput, result, err)
	}
	if jsonOutput {
		return cli.WriteJSON(cmd, sessionActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data})
	}
	if result.Message != "" {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
	}
	return nil
}

func sessionActionCommandError(cmd *cobra.Command, jsonOutput bool, result actions.Result, err error) error {
	var partial *actions.PartialResultError
	if errors.As(err, &partial) {
		if jsonOutput {
			return cli.WriteJSONPartial(cmd, sessionActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data}, partial)
		}
		if result.Message != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
		}
		return partial
	}
	if !jsonOutput {
		return err
	}
	var actionErr *actions.Error
	if errors.As(err, &actionErr) {
		return cli.WriteJSONError(cmd, cli.JSONError{Code: actionErr.Code, Message: actionErr.Message})
	}
	return cli.CommandFailure(cmd, true, "action_failed", err)
}

func parseConversationSelector(value string) (conversation.Identity, bool) {
	harnessName, sessionID, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found {
		return conversation.Identity{}, false
	}
	return conversation.New(harnessName, sessionID)
}

type sessionListData struct {
	Sessions []sessionView `json:"sessions"`
}

type sessionActionData struct {
	Action  actions.ID     `json:"action"`
	Changed bool           `json:"changed"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
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
}

func sessionViews(items []transcript.SessionMeta) []sessionView {
	out := make([]sessionView, 0, len(items))
	for _, item := range items {
		out = append(out, sessionView{ID: item.ID, Harness: item.Harness, Profile: item.DisplayProfile(), Profiles: append([]string{}, item.ProfileNames()...), Project: item.Project, Projects: append([]string{}, item.ProjectNames()...), CWD: item.CWD, StartedAt: item.StartedAt, LastAt: item.LastAt, Path: item.Path, FileSize: item.FileSize, FileModTime: item.FileModTime, MessageCount: item.MessageCount, Snippet: item.Snippet})
	}
	return out
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}
