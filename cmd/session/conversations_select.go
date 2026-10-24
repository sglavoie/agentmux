package session

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

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
	offset int
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
	selection.offset, _ = cmd.Flags().GetInt("offset")
	if selection.offset < 0 {
		return selection, "invalid_arguments", fmt.Errorf("--offset must be nonnegative")
	}
	if exact, _ := cmd.Flags().GetString("conversation"); exact != "" {
		h, id, found := strings.Cut(exact, ":")
		identity, valid := conversation.New(h, id)
		if !found || !valid {
			return selection, "invalid_arguments", fmt.Errorf("--conversation requires an exact harness:session-id")
		}
		selection.filter.Conversation = transcript.IndexKey(identity.Harness, identity.SessionID)
	}
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
	metas = metas[min(s.offset, len(metas)):]
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
			return unknownProjectError(selector, names)
		}
	}
	return nil
}

// projectSuggestionLimit bounds the names an unknown --project suggests, so a
// one-letter value that most titles contain stays a one-line error.
const projectSuggestionLimit = 3

// unknownProjectError refuses a --project value that names nothing, offering
// the names it most likely meant, as `amx project launch` does for a title.
// The suggestions are never selected on the caller's behalf.
func unknownProjectError(selector string, names []string) error {
	message := fmt.Sprintf("--project %q: no project or indexed session has this title", selector)
	near := config.ProjectTitleNearMisses(names, selector)
	if len(near) == 0 {
		return errors.New(message + "; `amx project list` shows the configured ones")
	}
	quoted := make([]string, 0, projectSuggestionLimit)
	for _, name := range near[:min(len(near), projectSuggestionLimit)] {
		quoted = append(quoted, strconv.Quote(name))
	}
	suggestion := strings.Join(quoted, ", ")
	if len(quoted) > 1 {
		suggestion = strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
	if more := len(near) - len(quoted); more > 0 {
		suggestion += fmt.Sprintf(" (and %d more)", more)
	}
	return errors.New(message + "; did you mean " + suggestion + "?")
}

// addSessionSelectionFlags declares the selector flags readSessionSelection
// reads, so list and search cannot drift apart in what they accept.
func addSessionSelectionFlags(cmd *cobra.Command) {
	cmd.Flags().String("since", "", "only sessions active within this window, e.g. 12h, 7d, or 2w")
	cmd.Flags().Bool("here", false, "only sessions started in the current directory or below it")
	cmd.Flags().Bool("live", false, "only conversations open right now, in a Kitty tab or a tmux session")
	cmd.Flags().StringSlice("harness", nil, "only sessions of this harness (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("harness", cli.CompleteHarnesses)
	cmd.Flags().StringSlice("profile", nil, "only sessions visible to this profile (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	cmd.Flags().StringSlice("project", nil, "only sessions of this project, by title; \"amx\" also matches \"🐱amx\" (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("project", completeSessionProjects)
	cmd.Flags().Int("offset", 0, "skip this many matching sessions before applying --limit")
	cmd.Flags().String("conversation", "", "only this exact harness:session-id (no prefix matching)")
	cmd.Flags().IntP("limit", "n", 0, "show at most this many of the most recent sessions (0 for all; a terminal shows 25 unless given)")
	cmd.Flags().BoolP("all", "a", false, "include automated sessions (ASL runs, subagents, exec) and unclassified ones")
}

// hiddenAutomated is how many more sessions the selection would find with
// --all: the automated and unclassified ones it leaves out, under every
// other selector it was given and regardless of --limit. It is 0 with --all.
func hiddenAutomated(selection sessionSelection, idx *transcript.Index) int {
	if !selection.filter.MainOnly {
		return 0
	}
	selection.limit, selection.offset = 0, 0
	all := selection.filter
	all.MainOnly = false
	return len(selection.narrow(idx.List(all))) - len(selection.narrow(idx.List(selection.filter)))
}

// noSessionsMessage is an empty list's sentence for a person. It names the
// selector flags that emptied the list, as typed, and, when the list hid
// automated sessions, says whether --all would have found any.
//
// --here on its own says where it looked, in the sentence `session continue
// --here` refuses with, since "match --here" does not say which directory
// that was.
func noSessionsMessage(cmd *cobra.Command, selection sessionSelection, idx *transcript.Index) string {
	automated := hiddenAutomated(selection, idx) > 0
	flags := selectionFlagsText(cmd)
	if flags == "--here" && len(selection.here) > 0 {
		message := "No conversation was started in " + cli.DisplayPath(selection.here[0]) + " or below it"
		if automated {
			return message + "; automated sessions were, and --all shows them."
		}
		return message + "; `amx session list` shows recent ones elsewhere."
	}
	message := "No indexed sessions match."
	if flags != "" {
		message = "No indexed sessions match " + flags + "."
	}
	if automated {
		message += " Automated sessions do; --all shows them."
	}
	return message
}

// selectionFlagsText is the selector flags the command was given, written
// back as flags, in the order --help lists them.
func selectionFlagsText(cmd *cobra.Command) string {
	var parts []string
	for _, name := range []string{"since", "here", "live", "harness", "profile", "project"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || !flag.Changed {
			continue
		}
		switch flag.Value.Type() {
		case "bool":
			if flag.Value.String() == "true" {
				parts = append(parts, "--"+name)
			}
		case "stringSlice":
			if values, _ := cmd.Flags().GetStringSlice(name); len(values) > 0 {
				parts = append(parts, "--"+name+" "+shellWord(strings.Join(values, ",")))
			}
		default:
			parts = append(parts, "--"+name+" "+shellWord(flag.Value.String()))
		}
	}
	return strings.Join(parts, " ")
}

// shellWord quotes a flag value that would not survive being pasted back
// into a shell as one word.
func shellWord(value string) string {
	if value == "" || strings.ContainsAny(value, " \t'\"\\$`") {
		return strconv.Quote(value)
	}
	return value
}

// liveConversations is what list and search mark rows live from: the tabs
// --live already asked Kitty for, else a fresh ask when the output shows it
// (always for list, and for search a terminal table or JSON, since its piped
// rows have no LIVE field). known reports whether anyone answered, so a table
// that could not ask leaves the column out, and a piped list row says "?",
// rather than showing every row as closed.
func (s sessionSelection) liveConversations(shown bool) (live map[string]liveConversation, known bool) {
	if s.live != nil {
		// --live already asked, and was refused had nobody answered.
		return s.live, true
	}
	if !shown {
		return nil, false
	}
	return sessionCommandsLiveConversations()
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
			return cli.UnknownProfile(cfg, name)
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
