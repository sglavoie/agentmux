package session

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/spf13/cobra"
)

func newConversationActionCommand(name string, actionID actions.ID, short, long, example string) *cobra.Command {
	return &cobra.Command{
		Use:               name + " <harness> <session-id> | <harness>:<session-id> | <session-id>",
		ValidArgsFunction: completeConversation,
		Short:             short,
		Long:              long,
		Example:           example,
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
		summary := layout.Truncate(oneLine(meta.ShortSummary()), sessionSummaryWidth, "…")
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Picked %s:%s, active %s: %s\n", meta.Harness, meta.ID, formatAgo(meta.LastAt, sessionCommandsNow()), summary)
	}
	return executeSessionAction(cmd, jsonOutput, cfg, actionID, actions.Request{Conversation: &meta})
}

// conversationParameterDescription names every selector form a continuity
// command accepts, so a missing conversation is reported the same way whether
// the command takes one selector or the two-argument form too.
// conversationSelectorHelp is how the help of a command taking a
// conversation says what may name one, after "A selector is one of".
const conversationSelectorHelp = "`<harness> <session-id>`, `<harness>:<session-id>`, or a bare\nsession ID or unique prefix of at least four characters (see `amx session\nlist`)"

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
		switch {
		case identity.Harness == "" && configuredProjectTitle(cfg, identity.SessionID) != "":
			title := configuredProjectTitle(cfg, identity.SessionID)
			hint = fmt.Sprintf("%s is a configured project, not a conversation; `amx session list --project %s` lists its conversations by ID", title, shellWord(identity.SessionID))
		case !looksLikeSessionID(identity.SessionID):
			hint = fmt.Sprintf("to find a conversation by what was said in it, run `amx session search %s`", shellWord(identity.SessionID))
		case len(identity.SessionID) < sessionIDPrefixMin:
			hint = fmt.Sprintf("a session ID prefix needs at least %d characters", sessionIDPrefixMin)
		}
		return nil, cli.CommandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s was not found; %s", selector, hint))
	}
	return meta, nil
}

// configuredProjectTitle returns the title of the configured project a
// selector names, by the rule --project reads a title with (exact, or once
// leading emoji or symbols and case are set aside), or "" when it names none
// or several. A continuity selector is an ID, so this only shapes the hint for
// one that is a project instead.
func configuredProjectTitle(cfg *config.Config, selector string) string {
	if cfg == nil {
		return ""
	}
	selector = strings.TrimSpace(selector)
	key := config.ProjectTitleKey(selector)
	title := ""
	for _, project := range cfg.Projects {
		if project.Title == selector {
			return project.Title
		}
		if key != "" && config.ProjectTitleKey(project.Title) == key {
			if title != "" && title != project.Title {
				return ""
			}
			title = project.Title
		}
	}
	return title
}

// looksLikeSessionID reports whether a selector could be a session ID or a
// prefix of one. Every harness names its conversations with UUIDs, so anything
// else is more likely a word the operator remembers from the conversation,
// which search can find and an ID lookup never will.
func looksLikeSessionID(selector string) bool {
	for _, r := range selector {
		if r != '-' && !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return false
		}
	}
	return selector != ""
}

// sessionIDPrefixMin is the shortest session ID prefix a continuity command
// accepts in place of the whole ID. Shorter prefixes would match most of the
// index and answer with a candidate list longer than the list command's.
const sessionIDPrefixMin = 4

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
	// Newest first: a text caller is shown only the first few candidates, and
	// the conversation meant is far more often a recent one than the
	// alphabetically first. The ID breaks ties so the order stays stable.
	sort.Slice(matches, func(i, j int) bool {
		if !matches[i].LastAt.Equal(matches[j].LastAt) {
			return matches[i].LastAt.After(matches[j].LastAt)
		}
		return matches[i].Harness+":"+matches[i].ID < matches[j].Harness+":"+matches[j].ID
	})
	candidates := make([]actions.Candidate, 0, len(matches))
	for _, meta := range matches {
		candidates = append(candidates, actions.Candidate{ID: meta.Harness + ":" + meta.ID, DisplayName: meta.Project, CanonicalPath: meta.CWD})
	}
	if len(exact) > 1 {
		return nil, &actions.Error{
			Code:              "ambiguous_conversation",
			Message:           fmt.Sprintf("%d indexed conversations have session ID %q; qualify it with its harness", len(matches), identity.SessionID),
			MissingParameters: []actions.ParameterSchema{{Name: "harness", Type: "string", Description: "qualify the ID as <harness>:<session-id> (claude, codex, or copilot)", Required: true, Positional: true}},
			Candidates:        candidates,
		}
	}
	return nil, &actions.Error{
		Code:              "ambiguous_conversation",
		Message:           fmt.Sprintf("%d indexed conversations have a session ID starting with %q; give more of it", len(matches), identity.SessionID),
		MissingParameters: []actions.ParameterSchema{{Name: "session-id", Type: "string", Description: "a longer prefix of the session ID, or all of it", Required: true, Positional: true}},
		Candidates:        candidates,
	}
}

func executeSessionAction(cmd *cobra.Command, jsonOutput bool, cfg *config.Config, actionID actions.ID, request actions.Request) error {
	deps := actions.Dependencies{
		Config: cfg, Readers: sessionCommandsSessionReaders(), Daemon: sessionCommandsDaemon, Handoff: sessionCommandsNewHandoff(), Worktree: sessionCommandsNewWorktree(),
	}
	if err := cli.SelectBackend(cmd, &deps); err != nil {
		return sessionActionCommandError(cmd, jsonOutput, actions.Result{Action: actionID}, err)
	}
	// A tmux launch must work with no Kitty running, so it never reads Kitty.
	if deps.Backend != runtimesession.BackendTmux {
		deps.Kitty = sessionCommandsNewKitty()
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

type sessionActionData struct {
	Action  actions.ID     `json:"action"`
	Changed bool           `json:"changed"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}
