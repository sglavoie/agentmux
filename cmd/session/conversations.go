package session

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/layout"
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
	sessionListGetwd                = os.Getwd
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
		filter := transcript.ListFilter{}
		if value, _ := cmd.Flags().GetString("since"); strings.TrimSpace(value) != "" {
			window, err := parseSinceWindow(value)
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", err)
			}
			filter.Since = sessionCommandsNow().Add(-window)
		}
		if err := applySessionListSelectors(cmd, cfg, &filter); err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", err)
		}
		limit, _ := cmd.Flags().GetInt("limit")
		if limit < 0 {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("--limit %d: want 0 (no limit) or a positive count", limit))
		}
		var here []string
		if onlyHere, _ := cmd.Flags().GetBool("here"); onlyHere {
			if here, err = sessionListHereDirs(); err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "command_failed", err)
			}
		}
		idx, err := loadRefreshedSessionIndex(cfg, sessionCommandsSessionReaders())
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		metas := idx.List(filter)
		if here != nil {
			metas = sessionsUnder(metas, here)
		}
		if limit > 0 && len(metas) > limit {
			metas = metas[:limit]
		}
		views := sessionViews(metas)
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionListData{Sessions: views})
		}
		out := cmd.OutOrStdout()
		if sessionListIsTerminal(out) {
			return writeSessionTable(out, views, sessionCommandsNow())
		}
		// Off a terminal the output stays headerless and tab-separated with
		// absolute timestamps: scripts cut and sort it, and a relative age is
		// stale the moment it is written to a file.
		for _, item := range views {
			_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", item.Harness, item.Profile, item.Project, formatTime(item.LastAt), item.ID)
		}
		return nil
	},
}

// writeSessionTable is the session list for a person: a header, aligned
// columns, and how long ago each conversation was last active.
func writeSessionTable(out io.Writer, views []sessionView, now time.Time) error {
	if len(views) == 0 {
		_, err := fmt.Fprintln(out, "No indexed sessions match.")
		return err
	}
	rows := make([][]string, 0, len(views))
	for _, item := range views {
		project := item.Project
		if project == "" {
			project = "-"
		}
		summary := layout.Truncate(item.Snippet, sessionSummaryWidth, "…")
		if summary == "" {
			summary = "-"
		}
		rows = append(rows, []string{item.Harness, item.Profile, project, formatAgo(item.LastAt, now), item.ID, summary})
	}
	return cli.WriteTable(out, []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "SESSION", "SUMMARY"}, rows)
}

// sessionSummaryWidth bounds the table's last column. Several sessions of one
// project differ only by ID, and the opening prompt is what tells them apart;
// a fixed cut keeps a row on one line in an ordinary terminal without asking
// the terminal for its width.
const sessionSummaryWidth = 60

// applySessionListSelectors turns --harness and --profile into the index's own
// filter. An unknown name is refused rather than matched against nothing,
// since an empty list would read as "no sessions" instead of "no such name".
func applySessionListSelectors(cmd *cobra.Command, cfg *config.Config, filter *transcript.ListFilter) error {
	harnesses, _ := cmd.Flags().GetStringSlice("harness")
	for _, name := range harnesses {
		known := harness.NormalizeKnown(name)
		if known == "" {
			return fmt.Errorf("--harness %q: want one of %s", name, strings.Join(harness.Names(), ", "))
		}
		filter.Harness = append(filter.Harness, known)
	}
	profiles, _ := cmd.Flags().GetStringSlice("profile")
	for _, name := range profiles {
		if !configHasProfile(cfg, name) {
			return fmt.Errorf("--profile %q: not a configured profile", name)
		}
		filter.Profile = append(filter.Profile, name)
	}
	return nil
}

func configHasProfile(cfg *config.Config, name string) bool {
	for _, profile := range cfg.Profiles {
		if profile.Name == name {
			return true
		}
	}
	return false
}

// sessionListHereDirs is the working directory as --here compares it: both
// as the shell reports it and with symlinks resolved, because a harness
// records whichever form it was started from.
func sessionListHereDirs() ([]string, error) {
	wd, err := sessionListGetwd()
	if err != nil {
		return nil, fmt.Errorf("--here: %w", err)
	}
	dirs := []string{filepath.Clean(wd)}
	if resolved, err := filepath.EvalSymlinks(wd); err == nil && filepath.Clean(resolved) != dirs[0] {
		dirs = append(dirs, filepath.Clean(resolved))
	}
	return dirs, nil
}

// sessionsUnder keeps the sessions whose working directory is one of dirs or
// lies inside one, so --here run at a repository root also finds the
// sessions started in its subdirectories.
func sessionsUnder(metas []transcript.SessionMeta, dirs []string) []transcript.SessionMeta {
	out := metas[:0:0]
	for _, meta := range metas {
		if meta.CWD == "" {
			continue
		}
		cwd := filepath.Clean(meta.CWD)
		for _, dir := range dirs {
			if cwd == dir || strings.HasPrefix(cwd, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator)) {
				out = append(out, meta)
				break
			}
		}
	}
	return out
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

// parseSinceWindow reads `--since`: any Go duration ("12h", "90m") or a whole
// number of days ("7d"), which Go's own parser has no unit for and which is
// the window people actually think in for past sessions.
func parseSinceWindow(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if days, ok := strings.CutSuffix(value, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("--since %q: want a positive whole number of days like 7d", value)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	window, err := time.ParseDuration(value)
	if err != nil || window <= 0 {
		return 0, fmt.Errorf("--since %q: want a positive duration like 12h or 7d", value)
	}
	return window, nil
}

var sessionContinueCmd = newConversationActionCommand("continue", actions.Continue, "Focus an exact live conversation or resume its indexed source")
var sessionForkCmd = newConversationActionCommand("fork", actions.Fork, "Fork an indexed conversation (portable for Copilot)")

var sessionForkWorktreeCmd = &cobra.Command{
	Use:               "fork-worktree <harness> <session-id> | <harness>:<session-id> | <session-id>",
	ValidArgsFunction: cli.CompleteHarnessFirstArg,
	Short:             "Fork an indexed conversation in a new managed Git worktree",
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
	Use:   "handoff <harness>:<session-id> | <session-id>",
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
			return cli.MissingParameter(cmd, jsonOutput, "conversation", "indexed conversation as <harness>:<session-id> or a bare session ID")
		}
		if len(args) != 1 {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("session handoff accepts exactly one conversation selector"))
		}
		if strings.TrimSpace(profile) == "" {
			return cli.MissingParameter(cmd, jsonOutput, "profile", "target configured profile")
		}
		identity, err := conversationIdentityFromArgs(cmd, jsonOutput, "handoff", args)
		if err != nil {
			return err
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
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

func newConversationActionCommand(name string, actionID actions.ID, short string) *cobra.Command {
	return &cobra.Command{
		Use:               name + " <harness> <session-id> | <harness>:<session-id> | <session-id>",
		ValidArgsFunction: cli.CompleteHarnessFirstArg,
		Short:             short,
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
			meta, err := resolveConversationOrFail(cmd, jsonOutput, cfg, sessionCommandsSessionReaders(), identity)
			if err != nil {
				return err
			}
			return executeSessionAction(cmd, jsonOutput, cfg, actionID, actions.Request{Conversation: meta})
		},
	}
}

// conversationIdentityFromArgs reads the conversation a continuity command
// acts on, either as two arguments (`claude abc`), as the single `claude:abc`
// selector, or as a bare session ID, so a selector copied from one command
// works in the others. A bare session ID comes back with an empty harness for
// resolveConversationOrFail to settle against the index; a lone argument that
// names a harness is still read as a harness whose session ID is missing.
func conversationIdentityFromArgs(cmd *cobra.Command, jsonOutput bool, name string, args []string) (conversation.Identity, error) {
	if len(args) == 1 && strings.Contains(args[0], ":") {
		identity, ok := parseConversationSelector(args[0])
		if !ok {
			return conversation.Identity{}, cli.CommandFailure(cmd, jsonOutput, "invalid_selector", errors.New("conversation must be <harness>:<session-id> or a bare session ID"))
		}
		return identity, nil
	}
	if len(args) == 1 && strings.TrimSpace(args[0]) != "" && !harness.Valid(args[0]) {
		return conversation.Identity{SessionID: strings.TrimSpace(args[0])}, nil
	}
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

// resolveConversationOrFail turns a parsed selector into the indexed
// conversation it names, reporting a miss, an unreadable index, or an
// ambiguous bare session ID in the caller's output mode.
func resolveConversationOrFail(cmd *cobra.Command, jsonOutput bool, cfg *config.Config, readers map[string]transcript.Reader, identity conversation.Identity) (*transcript.SessionMeta, error) {
	meta, err := resolveIndexedConversation(cfg, readers, identity)
	if err != nil {
		var actionErr *actions.Error
		if errors.As(err, &actionErr) {
			return nil, sessionActionCommandError(cmd, jsonOutput, actions.Result{}, err)
		}
		return nil, cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
	}
	if meta == nil {
		selector := identity.SessionID
		if identity.Harness != "" {
			selector = identity.Harness + ":" + identity.SessionID
		}
		return nil, cli.CommandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s was not found", selector))
	}
	return meta, nil
}

// resolveIndexedConversation looks an exact identity up in the index. An
// identity without a harness is a bare session ID: it resolves only when
// exactly one visible conversation carries that ID, and several matches are
// refused with their harness-qualified selectors rather than guessed between.
func resolveIndexedConversation(cfg *config.Config, readers map[string]transcript.Reader, identity conversation.Identity) (*transcript.SessionMeta, error) {
	idx, err := loadRefreshedSessionIndex(cfg, readers)
	if err != nil {
		return nil, err
	}
	if identity.Harness == "" {
		var matches []transcript.SessionMeta
		for _, meta := range idx.List(transcript.ListFilter{}) {
			if meta.ID == identity.SessionID {
				matches = append(matches, meta)
			}
		}
		switch len(matches) {
		case 0:
			return nil, nil
		case 1:
			return &matches[0], nil
		}
		candidates := make([]actions.Candidate, 0, len(matches))
		for _, meta := range matches {
			candidates = append(candidates, actions.Candidate{ID: meta.Harness + ":" + meta.ID, DisplayName: meta.Project, CanonicalPath: meta.CWD})
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		return nil, &actions.Error{
			Code:              "ambiguous_conversation",
			Message:           fmt.Sprintf("%d indexed conversations have session ID %q; qualify it with its harness", len(matches), identity.SessionID),
			MissingParameters: []actions.ParameterSchema{{Name: "harness", Type: "string", Description: "source harness (claude, codex, or copilot)", Required: true}},
			Candidates:        candidates,
		}
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
	var actionErr *actions.Error
	if !jsonOutput {
		if errors.As(err, &actionErr) {
			return cli.ActionErrorText(actionErr)
		}
		return err
	}
	if errors.As(err, &actionErr) {
		payload := cli.JSONError{Code: actionErr.Code, Message: actionErr.Message}
		for _, candidate := range actionErr.Candidates {
			payload.Candidates = append(payload.Candidates, cli.Candidate(candidate))
		}
		for _, parameter := range actionErr.MissingParameters {
			payload.Parameters = append(payload.Parameters, cli.ParameterHelp{Name: parameter.Name, Description: parameter.Description, Required: parameter.Required})
		}
		return cli.WriteJSONError(cmd, payload)
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
