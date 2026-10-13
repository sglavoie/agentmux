package tmux

import (
	"regexp"
	"strings"
	"unicode"
)

// What a session shows about itself in a terminal is two options on its
// agent's pane, which the configuration builds the outer terminal's title and
// the status line from. AgentMux's daemon renders them; nothing else writes
// them.
const (
	// StatusOption holds the rendered state icon and title.
	StatusOption = "@amx_status"
	// StatusColorOption holds the session's color as #rrggbb, empty for none.
	StatusColorOption = "@amx_status_color"
)

// WorkspaceHostEnv is set, to anything, in the environment of an `amx attach`
// the desktop workspace runs in one of its own terminals. It never reaches a
// pane: a terminal opened inside a session is not hosted by anything.
const WorkspaceHostEnv = "AMX_WORKSPACE_HOSTED"

// Status is the rendered status of the session whose agent runs on one pane
// of one server generation.
type Status struct {
	Pane       string
	Generation string
	// Text is the state icon and title on one line.
	Text string
	// Color is the session's color as #rrggbb. Anything else is no color.
	Color string
}

var statusColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// WriteStatuses writes every status in one client invocation, however many
// there are. Each is applied only while the server is still the generation it
// names, so a pane ID a restarted server reused is never labeled as the
// session that used to hold it, and each is applied by itself: a pane that is
// gone is reported in the error and costs the others nothing.
func (b *Backend) WriteStatuses(statuses []Status) error {
	var chain [][]string
	for _, status := range statuses {
		if !canonicalID(status.Pane, '%') || status.Generation == "" || strings.ContainsAny(status.Generation, "#{},'") {
			continue
		}
		color := status.Color
		if !statusColor.MatchString(color) {
			color = ""
		}
		set := "set-option -p -t " + status.Pane + " "
		chain = append(chain, []string{
			"if-shell", "-F", "#{==:#{pid}-#{start_time}," + status.Generation + "}",
			set + StatusOption + " " + quoteArg(statusText(status.Text)) +
				" ; " + set + StatusColorOption + " " + quoteArg(color),
		})
	}
	if len(chain) == 0 {
		return nil
	}
	_, err := b.server.runChain(chain)
	return err
}

// statusText is text as one line a terminal title can carry: no control
// characters, its runs of whitespace single spaces.
func statusText(text string) string {
	return strings.Join(strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
}

// quoteArg is arg as one word of a tmux command line. Inside single quotes
// tmux reads every character as itself.
func quoteArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}
