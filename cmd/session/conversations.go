package session

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	sessionListTerminalWidth        = cli.TerminalWidth
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
		metas := selection.narrow(idx.List(selection.filter))
		views := sessionViews(metas)
		out := cmd.OutOrStdout()
		terminal := !jsonOutput && sessionListIsTerminal(out)
		liveKnown := false
		if selection.live != nil {
			// --live already asked, and was refused had nobody answered.
			markLive(views, selection.live, sessionCommandsCurrentWindow())
			liveKnown = true
		} else if jsonOutput || terminal {
			var live map[string]liveConversation
			live, liveKnown = sessionCommandsLiveConversations()
			markLive(views, live, sessionCommandsCurrentWindow())
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionListData{Sessions: views})
		}
		if terminal {
			return writeSessionTable(out, views, abbreviateSessionIDs(idx.List(transcript.ListFilter{})), sessionCommandsNow(), sessionListTerminalWidth(out), liveKnown)
		}
		writeSessionLines(out, views)
		return nil
	},
}

// writeSessionLines is `session list` off a terminal: headerless and
// tab-separated with absolute timestamps, because scripts cut and sort it and
// a relative age is stale the moment it is written to a file. The opening
// prompt and then the title follow the fixed fields, each on one line, so a
// picker such as fzf has something to match on. The title came last, after
// the prompt, so every field before it kept its position; it is empty when the
// harness named the conversation nothing.
func writeSessionLines(out io.Writer, views []sessionView) {
	for _, item := range views {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.Harness, item.Profile, item.Project, formatTime(item.LastAt), item.ID, oneLine(item.Snippet), oneLine(item.Title))
	}
}

// sessionSelection is what the selector flags `session list` and `session
// search` share ask for: the index filter, the --here directories, and the
// --limit count, which the two apply after their own lookup. Without --all the
// filter keeps only sessions positively classified as interactive, the same
// rows Past Sessions shows; continuity commands resolve any session by ID
// regardless.
type sessionSelection struct {
	filter transcript.ListFilter
	here   []string
	limit  int
	// projectSelectors are the --project values as typed, and projects the
	// project names resolveProjects found for them; narrow keeps the sessions
	// belonging to any of those names.
	projectSelectors []string
	projects         []string
	// live is the conversations open in Kitty when --live asked for only
	// those, and nil otherwise.
	live map[string]liveConversation
}

// readSessionSelection parses the shared selector flags. The returned code is
// the structured error code for a refusal.
func readSessionSelection(cmd *cobra.Command, cfg *config.Config) (sessionSelection, string, error) {
	var selection sessionSelection
	if value, _ := cmd.Flags().GetString("since"); strings.TrimSpace(value) != "" {
		window, err := parseSinceWindow(value)
		if err != nil {
			return selection, "invalid_arguments", err
		}
		selection.filter.Since = sessionCommandsNow().Add(-window)
	}
	if err := applySessionListSelectors(cmd, cfg, &selection.filter); err != nil {
		return selection, "invalid_arguments", err
	}
	all, _ := cmd.Flags().GetBool("all")
	selection.filter.MainOnly = !all
	selection.projectSelectors, _ = cmd.Flags().GetStringSlice("project")
	selection.limit, _ = cmd.Flags().GetInt("limit")
	if selection.limit < 0 {
		return selection, "invalid_arguments", fmt.Errorf("--limit %d: want 0 (no limit) or a positive count", selection.limit)
	}
	if onlyHere, _ := cmd.Flags().GetBool("here"); onlyHere {
		here, err := sessionListHereDirs()
		if err != nil {
			return selection, "command_failed", err
		}
		selection.here = here
	}
	if onlyLive, _ := cmd.Flags().GetBool("live"); onlyLive {
		live, known := sessionCommandsLiveConversations()
		if !known {
			// Printing no rows would claim nothing is open; the truth is that
			// nobody could say.
			return selection, "live_status_unavailable", errors.New("--live: cannot tell which conversations are open, because the daemon or Kitty did not answer; `amx doctor` says why")
		}
		if live == nil {
			live = map[string]liveConversation{}
		}
		selection.live = live
	}
	return selection, "", nil
}

// narrow applies --project, --here, --live, and then --limit to sessions
// already in the order they will be shown.
func (s sessionSelection) narrow(metas []transcript.SessionMeta) []transcript.SessionMeta {
	if s.projects != nil {
		kept := metas[:0:0]
		for _, meta := range metas {
			if meta.MatchesAnyProject(s.projects) {
				kept = append(kept, meta)
			}
		}
		metas = kept
	}
	if s.here != nil {
		metas = sessionsUnder(metas, s.here)
	}
	if s.live != nil {
		kept := metas[:0:0]
		for _, meta := range metas {
			if key, ok := liveConversationKey(meta.Harness, meta.ID); ok {
				if _, open := s.live[key]; open {
					kept = append(kept, meta)
				}
			}
		}
		metas = kept
	}
	if s.limit > 0 && len(metas) > s.limit {
		metas = metas[:s.limit]
	}
	return metas
}

// resolveProjects turns each --project value into the project names it
// selects, from the configured titles and the names sessions in the index
// already carry (a session outside every configured project is named after
// its directory). An exact name wins; failing one, a name that matches once
// leading emoji or symbols and case are set aside, as `amx project launch`
// reads a title, so "amx" selects "🐱amx". Unlike a launch, several such
// names are all kept, since a filter can show them together. A value naming
// nothing is refused, so a typo does not read as "no sessions".
func (s *sessionSelection) resolveProjects(cfg *config.Config, idx *transcript.Index) error {
	if len(s.projectSelectors) == 0 {
		return nil
	}
	var names []string
	known := map[string]bool{}
	add := func(name string) {
		if name != "" && !known[name] {
			known[name] = true
			names = append(names, name)
		}
	}
	for _, project := range cfg.Projects {
		add(project.Title)
	}
	for _, meta := range idx.List(transcript.ListFilter{IncludeHidden: true}) {
		for _, name := range meta.ProjectNames() {
			add(name)
		}
	}
	s.projects = []string{}
	for _, selector := range s.projectSelectors {
		selector = strings.TrimSpace(selector)
		if known[selector] {
			s.projects = append(s.projects, selector)
			continue
		}
		key := config.ProjectTitleKey(selector)
		matched := false
		for _, name := range names {
			if key != "" && config.ProjectTitleKey(name) == key {
				s.projects = append(s.projects, name)
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("--project %q: no project or indexed session has this title; `amx project list` shows the configured ones", selector)
		}
	}
	return nil
}

// addSessionSelectionFlags declares the selector flags readSessionSelection
// reads, so list and search cannot drift apart in what they accept.
func addSessionSelectionFlags(cmd *cobra.Command) {
	cmd.Flags().String("since", "", "only sessions active within this window, e.g. 12h or 7d")
	cmd.Flags().Bool("here", false, "only sessions started in the current directory or below it")
	cmd.Flags().Bool("live", false, "only conversations open in a Kitty tab right now")
	cmd.Flags().StringSlice("harness", nil, "only sessions of this harness (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("harness", cli.CompleteHarnessFirstArg)
	cmd.Flags().StringSlice("profile", nil, "only sessions visible to this profile (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	cmd.Flags().StringSlice("project", nil, "only sessions of this project, by title; \"amx\" also matches \"🐱amx\" (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("project", completeSessionProjects)
	cmd.Flags().IntP("limit", "n", 0, "show at most this many of the most recent sessions (0 for all)")
	cmd.Flags().BoolP("all", "a", false, "include automated sessions (ASL runs, subagents, exec) and unclassified ones")
}

// writeSessionTable is the session list for a person: a header, aligned
// columns, how long ago each conversation was last active, and each session
// ID cut to the prefix a continuity command still resolves. The summary is the
// harness's title for the conversation, else its opening prompt, and takes
// whatever the terminal, width cells wide, leaves it.
func writeSessionTable(out io.Writer, views []sessionView, ids sessionIDAbbreviations, now time.Time, width int, liveKnown bool) error {
	if len(views) == 0 {
		_, err := fmt.Fprintln(out, "No indexed sessions match.")
		return err
	}
	header := []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "SESSION", "SUMMARY"}
	if liveKnown {
		header = []string{"HARNESS", "PROFILE", "PROJECT", "ACTIVE", "LIVE", "SESSION", "SUMMARY"}
	}
	rows := make([][]string, 0, len(views))
	for _, item := range views {
		project := item.Project
		if project == "" {
			project = "-"
		}
		summary := oneLine(item.Title)
		if summary == "" {
			summary = oneLine(item.Snippet)
		}
		if summary == "" {
			summary = "-"
		}
		row := []string{item.Harness, item.Profile, project, formatAgo(item.LastAt, now)}
		if liveKnown {
			row = append(row, liveMark(item.Live))
		}
		rows = append(rows, append(row, ids.of(item.ID), summary))
	}
	fitLastColumn(header, rows, width)
	return cli.WriteTable(out, header, rows)
}

// liveMark is a list row's LIVE cell: a dot for a conversation open in a
// Kitty tab, where `session continue` focuses rather than resumes, and "here"
// for the one open in the window the command runs in, which is otherwise
// indistinguishable from its siblings when they share an opening prompt.
func liveMark(live *liveConversation) string {
	if live == nil {
		return ""
	}
	if live.Here {
		return "here"
	}
	return "●"
}

// sessionSummaryWidth bounds the table's last column when the terminal's width
// is unknown or leaves too little room. Several sessions of one project differ
// only by ID, and the opening prompt is what tells them apart; a fixed cut
// keeps a row on one line in an ordinary terminal.
const sessionSummaryWidth = 60

// fitLastColumn truncates each row's last cell to the room a terminal width
// cells wide leaves it, so a row stays on one line however wide the window.
func fitLastColumn(header []string, rows [][]string, width int) {
	limit := cli.LastColumnWidth(header, rows, width, sessionSummaryWidth)
	for _, row := range rows {
		last := len(row) - 1
		row[last] = layout.Truncate(row[last], limit, "…")
	}
}

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
	ValidArgsFunction: completeConversation,
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
	Use:               "handoff <harness>:<session-id> | <session-id>",
	ValidArgsFunction: completeOneConversation,
	Short:             "Launch a fresh target profile from a portable handoff pack",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		edit, _ := cmd.Flags().GetBool("edit")
		noLaunch, _ := cmd.Flags().GetBool("no-launch")
		profile, _ := cmd.Flags().GetString("profile")
		if edit && jsonOutput {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_options", errors.New("--edit cannot be used with --json"))
		}
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return cli.MissingParameter(cmd, jsonOutput, "conversation", conversationParameterDescription)
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
		ValidArgsFunction: completeConversation,
		Short:             short,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			// Only the commands that declare --here read it; for the others
			// GetBool reports the flag as absent and false.
			if here, _ := cmd.Flags().GetBool("here"); here {
				return runConversationActionHere(cmd, jsonOutput, name, actionID, args)
			}
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

// runConversationActionHere is `session continue --here`: the action on the
// most recently active interactive conversation started in the current
// directory or below it, the same rows `session list --here` shows first. It
// does not guess between candidates — "most recent" is the rule the flag
// names — but it says which conversation it picked before acting, so the
// choice is never invisible. A selector beside the flag is refused rather than
// one of the two silently winning.
func runConversationActionHere(cmd *cobra.Command, jsonOutput bool, name string, actionID actions.ID, args []string) error {
	if len(args) != 0 {
		return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("session %s --here picks the conversation itself; pass either --here or a selector", name))
	}
	cfg, err := sessionCommandsLoadConfig()
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
	}
	dirs, err := sessionListHereDirs()
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "command_failed", err)
	}
	idx, err := loadRefreshedSessionIndex(cfg, sessionCommandsSessionReaders())
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "session_index_unavailable", err)
	}
	candidates := sessionsUnder(idx.List(transcript.ListFilter{MainOnly: true}), dirs)
	if len(candidates) == 0 {
		return cli.CommandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("--here: no conversation was started in %s or below it; `amx session list` shows recent ones elsewhere", cli.DisplayPath(dirs[0])))
	}
	meta := candidates[0]
	if !jsonOutput {
		summary := oneLine(meta.Title)
		if summary == "" {
			summary = oneLine(meta.Snippet)
		}
		summary = layout.Truncate(summary, sessionSummaryWidth, "…")
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Picked %s:%s, active %s: %s\n", meta.Harness, meta.ID, formatAgo(meta.LastAt, sessionCommandsNow()), summary)
	}
	return executeSessionAction(cmd, jsonOutput, cfg, actionID, actions.Request{Conversation: &meta})
}

// conversationParameterDescription names every selector form a continuity
// command accepts, so a missing conversation is reported the same way whether
// the command takes one selector or the two-argument form too.
const conversationParameterDescription = "indexed conversation as <harness>:<session-id>, <harness> <session-id>, or a bare session ID or unique prefix (see `amx session list`)"

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
		return conversation.Identity{}, cli.MissingParameter(cmd, jsonOutput, "conversation", conversationParameterDescription)
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
	// list and search hide automated sessions by their class, so a legacy
	// entry that predates classification is classified first, exactly as the
	// Past Sessions view does, rather than vanishing as unclassified.
	migrateWarning := idx.MigrateSessionClasses(readers)
	if warning := errors.Join(loadWarning, refreshWarning, migrateWarning); warning != nil {
		// Recovering filesystem caches return usable snapshots with warning
		// errors. Keep continuity available while making degradation visible.
		slog.Warn("session index refresh completed with warnings", "path", idx.Path(), "err", warning)
	}
	return idx, nil
}

// resolveConversationOrFail turns a parsed selector into the indexed
// conversation it names, reporting a miss, an unreadable index, or an
// ambiguous session ID or prefix in the caller's output mode.
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
		hint := "run `amx session list` to see the indexed conversations"
		if len(identity.SessionID) < sessionIDPrefixMin {
			hint = fmt.Sprintf("a session ID prefix needs at least %d characters", sessionIDPrefixMin)
		}
		return nil, cli.CommandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s was not found; %s", selector, hint))
	}
	return meta, nil
}

// sessionIDPrefixMin is the shortest session ID prefix a continuity command
// accepts in place of the whole ID. Shorter prefixes would match most of the
// index and answer with a candidate list longer than the list command's.
const sessionIDPrefixMin = 4

// sessionIDDisplayMin is the shortest session ID a table prints. It is longer
// than sessionIDPrefixMin so a printed prefix rarely needs to grow when the
// index does.
const sessionIDDisplayMin = 8

// sessionIDAbbreviations maps each session ID to the shortest prefix, at least
// sessionIDDisplayMin characters, that resolveIndexedConversation reads back
// as that conversation alone. It is built from every visible indexed session
// across harnesses rather than from the rows on screen, because a pasted
// prefix is resolved against the whole index. An ID shared by two harnesses
// has no unique prefix and is printed whole. A nil value prints every ID
// whole.
type sessionIDAbbreviations map[string]string

func abbreviateSessionIDs(metas []transcript.SessionMeta) sessionIDAbbreviations {
	ids := make([]string, 0, len(metas))
	harnessOf := make(map[string]string, len(metas))
	shared := make(map[string]bool)
	for _, meta := range metas {
		ids = append(ids, meta.ID)
		if seen, ok := harnessOf[meta.ID]; ok && seen != meta.Harness {
			shared[meta.ID] = true
		}
		harnessOf[meta.ID] = meta.Harness
	}
	sort.Strings(ids)
	ids = slices.Compact(ids)
	out := make(sessionIDAbbreviations, len(ids))
	for i, id := range ids {
		if shared[id] {
			out[id] = id
			continue
		}
		common := 0
		if i > 0 {
			common = commonPrefixLen(id, ids[i-1])
		}
		if i+1 < len(ids) {
			common = max(common, commonPrefixLen(id, ids[i+1]))
		}
		out[id] = id[:min(len(id), max(sessionIDDisplayMin, common+1))]
	}
	return out
}

func (a sessionIDAbbreviations) of(id string) string {
	if short, ok := a[id]; ok {
		return short
	}
	return id
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// resolveIndexedConversation finds the one visible indexed conversation an
// identity names. An exact ID always wins; failing that, an ID of at least
// sessionIDPrefixMin characters is read as a prefix, the way a short Git hash
// is. An identity without a harness searches every harness. Either way, more
// than one match is refused with its harness-qualified selectors rather than
// settled by a guess.
func resolveIndexedConversation(cfg *config.Config, readers map[string]transcript.Reader, identity conversation.Identity) (*transcript.SessionMeta, error) {
	idx, err := loadRefreshedSessionIndex(cfg, readers)
	if err != nil {
		return nil, err
	}
	filter := transcript.ListFilter{}
	if identity.Harness != "" {
		filter.Harness = []string{identity.Harness}
	}
	var exact, prefixed []transcript.SessionMeta
	for _, meta := range idx.List(filter) {
		switch {
		case meta.ID == identity.SessionID:
			exact = append(exact, meta)
		case len(identity.SessionID) >= sessionIDPrefixMin && strings.HasPrefix(meta.ID, identity.SessionID):
			prefixed = append(prefixed, meta)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = prefixed
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
	if len(exact) > 1 {
		return nil, &actions.Error{
			Code:              "ambiguous_conversation",
			Message:           fmt.Sprintf("%d indexed conversations have session ID %q; qualify it with its harness", len(matches), identity.SessionID),
			MissingParameters: []actions.ParameterSchema{{Name: "harness", Type: "string", Description: "source harness (claude, codex, or copilot)", Required: true}},
			Candidates:        candidates,
		}
	}
	return nil, &actions.Error{
		Code:              "ambiguous_conversation",
		Message:           fmt.Sprintf("%d indexed conversations have a session ID starting with %q; give more of it", len(matches), identity.SessionID),
		MissingParameters: []actions.ParameterSchema{{Name: "session-id", Type: "string", Description: "a longer prefix of the session ID, or all of it", Required: true}},
		Candidates:        candidates,
	}
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
	Title        string    `json:"title,omitempty"`
	// Live is the Kitty tab the conversation is open in, when the daemon and
	// Kitty could both be asked and agreed on exactly one.
	Live *liveConversation `json:"live,omitempty"`
}

func sessionViews(items []transcript.SessionMeta) []sessionView {
	out := make([]sessionView, 0, len(items))
	for _, item := range items {
		out = append(out, sessionView{ID: item.ID, Harness: item.Harness, Profile: item.DisplayProfile(), Profiles: append([]string{}, item.ProfileNames()...), Project: item.Project, Projects: append([]string{}, item.ProjectNames()...), CWD: item.CWD, StartedAt: item.StartedAt, LastAt: item.LastAt, Path: item.Path, FileSize: item.FileSize, FileModTime: item.FileModTime, MessageCount: item.MessageCount, Snippet: item.Snippet, Title: item.Title})
	}
	return out
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}
