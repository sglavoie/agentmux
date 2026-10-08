package session

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// sessionCompletionLoadConfig is the loader completion reads projects
// through. Completion runs on every <Tab>, so it loads without validating and
// degrades to fewer suggestions rather than an error.
var sessionCompletionLoadConfig = config.LoadDefault

// sessionCompletionLimit bounds the conversations one <Tab> offers: enough to
// reach anything from today, few enough that the list fits a screen.
const sessionCompletionLimit = 20

// completionIndex is the transcript index as the last refresh left it.
// Completion never refreshes: a refresh walks every transcript, which a shell
// prompt cannot wait for, and a conversation started since the last list is
// still reachable by typing its ID.
func completionIndex() *transcript.Index {
	idx, _ := sessionCommandsLoadSessionIndex(sessionCommandsSessionIndexPath())
	return idx
}

// completeConversation suggests the selector a conversation command takes:
// harness names and the most recent conversations' session IDs for the first
// argument, and that harness's conversations for the second.
func completeConversation(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 1 || (len(args) == 1 && !harness.Valid(args[0])) {
		return nil, conversationDirective
	}
	if len(args) == 1 {
		return conversationSuggestions(args[0], toComplete), conversationDirective
	}
	var suggestions []string
	for _, name := range harness.Names() {
		if strings.HasPrefix(name, toComplete) {
			suggestions = append(suggestions, name)
		}
	}
	return append(suggestions, conversationSuggestions("", toComplete)...), conversationDirective
}

// completeOneConversation is completeConversation for a command that takes the
// selector as a single argument, where a bare harness name is not a start.
func completeOneConversation(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, conversationDirective
	}
	return conversationSuggestions("", toComplete), conversationDirective
}

// conversationDirective keeps the most recent conversation first rather than
// letting the shell sort the IDs, which would order them at random.
const conversationDirective = cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder

// conversationSuggestions lists the most recent conversations, of one harness
// when one is named. Each ID is the shortest prefix `session list` would
// print, described by the conversation's project and title, and is offered
// whole only once more than that prefix has been typed, so a shell filtering
// on what was typed still keeps it.
func conversationSuggestions(harnessName, toComplete string) []string {
	idx := completionIndex()
	if idx == nil {
		return nil
	}
	filter := transcript.ListFilter{MainOnly: true}
	if harnessName != "" {
		filter.Harness = []string{harnessName}
	}
	short := abbreviateSessionIDs(idx.List(transcript.ListFilter{}))
	var suggestions []string
	for _, meta := range idx.List(filter) {
		if len(suggestions) == sessionCompletionLimit {
			break
		}
		id := short.of(meta.ID)
		if !strings.HasPrefix(id, toComplete) {
			if !strings.HasPrefix(meta.ID, toComplete) {
				continue
			}
			id = meta.ID
		}
		suggestions = append(suggestions, id+"\t"+conversationDescription(meta))
	}
	return suggestions
}

// conversationDescription is the one line a completion menu shows beside an
// ID: where the conversation ran and what it was about.
func conversationDescription(meta transcript.SessionMeta) string {
	about := meta.Title
	if about == "" {
		about = meta.Snippet
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{meta.Harness, meta.Project, strings.Join(strings.Fields(about), " ")} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " · ")
}

// completeSessionProjects suggests what --project accepts: configured project
// titles and the project names indexed conversations carry, which include
// directories no project is configured for. Like a launch title, each is
// matched past its leading emoji (config.CompleteProjectTitle).
func completeSessionProjects(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		name, ok := config.CompleteProjectTitle(name, toComplete)
		if name != "" && ok && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if cfg, err := sessionCompletionLoadConfig(); err == nil && cfg != nil {
		for _, project := range cfg.Projects {
			add(project.Title)
		}
	}
	if idx := completionIndex(); idx != nil {
		for _, meta := range idx.List(transcript.ListFilter{}) {
			for _, name := range meta.ProjectNames() {
				add(name)
			}
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
