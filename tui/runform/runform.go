// Package runform is the Plans view's form for starting an
// agent-session-loop run of a plan: its state, how a key moves through it,
// and how it is drawn.
//
// It decides nothing about whether a plan may be started. The root package
// hands a submission to the shared plan.run action and shows what came back:
// either the run started, or agent-session-loop's own account of why it may
// not, finding by finding. What the form owns is the operator's side: which
// project and profile the supervising agent launches from, and the three
// choices passed on to `asl run`.
//
// Every row is a choice among a few values, so the form has no text field
// and nothing in it can be mistyped. A row whose first value is ASL's own
// default starts there, which leaves the command line as short as the
// operator's intent.
//
// The package is a leaf: it names no Model and imports no surface.
package runform

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

// Field is one row of the form.
type Field int

const (
	FieldProject Field = iota
	FieldProfile
	FieldHarness
	FieldReview
	FieldCommit
	fieldCount
)

// State is everything the form holds. It is replaced wholesale when the form
// closes.
type State struct {
	Active bool
	// Plan is the plan the form was opened on. It is never rewritten while
	// the form is open, so a refresh cannot retarget a run.
	Plan plans.PlanMeta
	// Projects are the projects the supervising session can launch from, and
	// Profiles every configured profile name. Neither is empty in an open form.
	Projects []config.Session
	Profiles []string
	// Project and Profile index those; Harness, Review, and Commit index the
	// row's values, where 0 is "left to agent-session-loop" (or no review).
	Project, Profile        int
	Harness, Review, Commit int
	// Field is the row the keyboard is on.
	Field Field
	// InFlight says a submission has not answered yet. Seq numbers the
	// submissions, so an answer to an earlier form is dropped.
	InFlight bool
	Seq      int
	// Readiness is agent-session-loop's account of the plan from the last
	// submission it refused, or nil.
	Readiness *planrun.Readiness
	// Notice is the last thing the form has to say, and Warn whether it is a
	// refusal or a failure.
	Notice string
	Warn   bool
}

// Open is a form for plan. projects are the launch candidates in the order
// they are offered; the form opens on the first, under its own profile.
func Open(plan plans.PlanMeta, projects []config.Session, profiles []string, seq int) State {
	state := State{Active: true, Plan: plan, Projects: projects, Profiles: profiles, Seq: seq}
	state.followProject()
	return state
}

// followProject puts the profile row on the selected project's own profile.
func (s *State) followProject() {
	if s.Project >= len(s.Projects) {
		return
	}
	own := s.Projects[s.Project].EffectiveProfile()
	for i, name := range s.Profiles {
		if strings.EqualFold(name, own) {
			s.Profile = i
			return
		}
	}
}

func harnessValues() []string { return harness.Names() }

func commitValues() []string {
	modes := planrun.CommitModes()
	values := make([]string, 0, len(modes))
	for _, mode := range modes {
		values = append(values, string(mode))
	}
	return values
}

// size is how many values a row cycles through.
func (s State) size(field Field) int {
	switch field {
	case FieldProject:
		return len(s.Projects)
	case FieldProfile:
		return len(s.Profiles)
	case FieldHarness, FieldReview:
		return len(harnessValues()) + 1
	case FieldCommit:
		return len(commitValues()) + 1
	}
	return 0
}

func (s *State) index(field Field) *int {
	switch field {
	case FieldProject:
		return &s.Project
	case FieldProfile:
		return &s.Profile
	case FieldHarness:
		return &s.Harness
	case FieldReview:
		return &s.Review
	default:
		return &s.Commit
	}
}

// Move steps the keyboard to another row, wrapping at either end.
func (s *State) Move(delta int) {
	s.Field = Field((int(s.Field) + delta + int(fieldCount)) % int(fieldCount))
}

// Cycle steps the current row to its next or previous value, wrapping. A
// changed choice clears what was said about the last one.
func (s *State) Cycle(delta int) {
	size := s.size(s.Field)
	if size < 2 {
		return
	}
	index := s.index(s.Field)
	*index = (*index + delta + size) % size
	if s.Field == FieldProject {
		s.followProject()
	}
	s.Readiness, s.Notice, s.Warn = nil, "", false
}

func optional(values []string, index int) string {
	if index <= 0 || index > len(values) {
		return ""
	}
	return values[index-1]
}

// Options are the choices passed on to `asl run`.
func (s State) Options() planrun.Options {
	return planrun.Options{
		Harness:       optional(harnessValues(), s.Harness),
		ReviewHarness: optional(harnessValues(), s.Review),
		CommitMode:    planrun.CommitMode(optional(commitValues(), s.Commit)),
	}
}

// SelectedProject is the project the supervising session launches from.
func (s State) SelectedProject() (config.Session, bool) {
	if s.Project >= len(s.Projects) {
		return config.Session{}, false
	}
	return s.Projects[s.Project], true
}

// Parameters are the form's choices as the plan.run action takes them,
// except the project, which the caller names by its identifier.
func (s State) Parameters() map[string]string {
	parameters := map[string]string{}
	if s.Profile < len(s.Profiles) {
		parameters["profile"] = s.Profiles[s.Profile]
	}
	opts := s.Options()
	for name, value := range map[string]string{"harness": opts.Harness, "review_harness": opts.ReviewHarness, "commit_mode": string(opts.CommitMode)} {
		if value != "" {
			parameters[name] = value
		}
	}
	return parameters
}

// PlanTitle is how the form names its plan.
func (s State) PlanTitle() string {
	if s.Readiness != nil && s.Readiness.Title != "" {
		return s.Readiness.Title
	}
	if s.Plan.Name != "" {
		return s.Plan.Name
	}
	return "plan"
}

const (
	innerWidth = 84
	labelWidth = 16
)

var (
	styleHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary)).Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
	styleWarning = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Amber))
	styleBox     = theme.Popup()
)

// Width is the inner width the form draws at on a terminal this wide.
func Width(terminalWidth int) int {
	width := innerWidth
	if avail := terminalWidth - 4; terminalWidth > 0 && avail < width {
		width = avail
	}
	return max(width, 1)
}

// Render draws the form inside the overlay border.
func Render(state State, terminalWidth int) string {
	width := Width(terminalWidth)
	return styleBox.Width(width).Render(strings.TrimRight(render(state, width), "\n"))
}

func (s State) value(field Field) string {
	switch field {
	case FieldProject:
		if s.Project >= len(s.Projects) {
			return "(none)"
		}
		project := s.Projects[s.Project]
		return fmt.Sprintf("%s (%s)", project.Title, project.EffectiveProfile())
	case FieldProfile:
		if s.Profile >= len(s.Profiles) {
			return "(none)"
		}
		return s.Profiles[s.Profile]
	case FieldHarness:
		if value := optional(harnessValues(), s.Harness); value != "" {
			return value
		}
		return "asl default"
	case FieldReview:
		if value := optional(harnessValues(), s.Review); value != "" {
			return value
		}
		return "none"
	default:
		if value := optional(commitValues(), s.Commit); value != "" {
			return value
		}
		return "asl default"
	}
}

var labels = [fieldCount]string{"Project", "Supervisor", "Harness", "Review harness", "Commit"}

func render(state State, width int) string {
	var lines []string
	// The box's border and padding come out of the width it is given.
	textWidth := max(width-styleBox.GetHorizontalFrameSize(), 1)
	add := func(style lipgloss.Style, text string) {
		for _, line := range wrapWords(text, textWidth) {
			lines = append(lines, style.Render(line))
		}
	}
	add(styleHeader, "Run "+state.PlanTitle())
	if state.Plan.CWD != "" {
		add(styleDim, "in "+state.Plan.CWD)
	}
	lines = append(lines, "")

	for field := Field(0); field < fieldCount; field++ {
		label := fmt.Sprintf("%-*s", labelWidth, labels[field])
		value := state.value(field)
		// A row is one line whatever its value: its columns are spacing.
		if field != state.Field {
			lines = append(lines, styleDim.Render(layout.Truncate("  "+label+value, textWidth, "…")))
			continue
		}
		if state.size(field) > 1 {
			value = "‹ " + value + " ›"
		}
		lines = append(lines, styleHeader.Render(layout.Truncate("▌ "+label+value, textWidth, "…")))
	}
	lines = append(lines, "")
	add(styleDim, planrun.Command(state.Plan.DirPath, state.Options()))

	switch {
	case state.InFlight:
		lines = append(lines, "", styleDim.Render("checking the plan…"))
	case state.Notice != "":
		lines = append(lines, "")
		style := styleDim
		if state.Warn {
			style = styleWarning
		}
		add(style, state.Notice)
	}
	if state.Readiness != nil {
		for _, finding := range state.Readiness.Findings {
			text := fmt.Sprintf("%s  %s: %s", finding.Severity, finding.Category, finding.Message)
			if finding.Detail != "" {
				text += " (" + finding.Detail + ")"
			}
			add(styleWarning, text)
		}
	}
	return strings.Join(lines, "\n")
}

// wrapWords breaks text at spaces to fit width, and inside a word only when
// the word alone is wider than a line, as a long path is.
func wrapWords(text string, width int) []string {
	var lines []string
	line := ""
	flush := func() {
		if line != "" {
			lines = append(lines, line)
			line = ""
		}
	}
	for _, word := range strings.Fields(text) {
		if line != "" && layout.Width(line)+1+layout.Width(word) <= width {
			line += " " + word
			continue
		}
		flush()
		if layout.Width(word) <= width {
			line = word
			continue
		}
		pieces := layout.WrapHard(word, width)
		lines = append(lines, pieces[:len(pieces)-1]...)
		line = pieces[len(pieces)-1]
	}
	flush()
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
