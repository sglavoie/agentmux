package project

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/spf13/cobra"
)

// projectView is one configured project as both leaves report it. Groups is
// always an array, empty rather than null, so a JSON caller can iterate it
// without a nil check. DirectoryProblem is filled by `list` only, and only
// when the directory is unusable; a launch from such a project fails on its
// own terms. Archived appears only under `list --archived`, the one place an
// archived project is shown.
type projectView struct {
	Profile          string   `json:"profile"`
	Title            string   `json:"title"`
	Harness          string   `json:"harness"`
	Directory        string   `json:"directory"`
	DirectoryProblem string   `json:"directory_problem,omitempty"`
	Groups           []string `json:"groups"`
	Archived         bool     `json:"archived,omitempty"`
}

// projectLaunchData is the `project launch --json` payload. TabID and WindowID
// name the tab the launch opened when exactly one new tab carrying the
// project's title appeared; they are omitted rather than guessed otherwise.
type projectLaunchData struct {
	Action   actions.ID  `json:"action"`
	Changed  bool        `json:"changed"`
	Message  string      `json:"message,omitempty"`
	Project  projectView `json:"project"`
	TabID    int         `json:"tab_id,omitempty"`
	WindowID int         `json:"window_id,omitempty"`
	// Backend is where the session launched; RuntimeSessionID is the runtime
	// session it registered (tmux launches), which `amx attach` takes.
	Backend          string `json:"backend"`
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
}

func runProjectList(cmd *cobra.Command, _ []string) error {
	cfg, err := projectLoadConfig()
	if err != nil {
		return cli.ConfigFailure(cmd, projectListJSON, err)
	}
	filter, err := newProjectListFilter(cfg)
	if err != nil {
		return cli.CommandFailure(cmd, projectListJSON, "invalid_arguments", err)
	}
	views := projectViews(cfg, projectListArchived)
	kept := views[:0]
	for _, view := range views {
		if !filter.keeps(view) {
			continue
		}
		view.DirectoryProblem = projectDirectoryProblem(view.Directory)
		if !projectListMissing || view.DirectoryProblem != "" {
			kept = append(kept, view)
		}
	}
	views = kept
	if projectListJSON {
		return cli.WriteJSON(cmd, views)
	}
	out := cmd.OutOrStdout()
	if !projectListIsTerminal(out) {
		writeProjectLines(out, views)
		return nil
	}
	note := archivedNote(cfg, filter)
	if len(views) == 0 {
		message := "No projects are configured; `amx config setup` sets up the first one."
		if note != "" {
			message = "Every configured project is archived."
		}
		switch {
		case projectListArchived:
			message = "No project is archived."
		case filter.active() && projectListMissing:
			message = "Every matching project directory is usable."
		case filter.active():
			message = "No configured project matches these filters."
		case projectListMissing:
			message = "Every configured project directory is usable."
		}
		_, err := fmt.Fprint(out, message+"\n"+note)
		return err
	}
	header := []string{"PROFILE", "TITLE", "HARNESS", "DIRECTORY", "GROUPS"}
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		directory := cli.DisplayPath(view.Directory)
		if view.DirectoryProblem != "" {
			directory += " (" + projectProblemLabel(view.DirectoryProblem) + ")"
		}
		rows = append(rows, []string{view.Profile, view.Title, view.Harness, directory, groupsField(view.Groups)})
	}
	fitProjectDirectories(header, rows, projectListTerminalWidth(out))
	if err := cli.WriteTable(out, header, rows); err != nil {
		return err
	}
	_, err = io.WriteString(out, note)
	return err
}

// archivedNote is the line under the table that says how many archived
// projects matching the filters the list left out, so they read as kept
// rather than gone. --archived lists them, and --missing never judges them,
// so neither has the note.
func archivedNote(cfg *config.Config, filter projectListFilter) string {
	if projectListArchived || projectListMissing {
		return ""
	}
	hidden := 0
	for _, view := range projectViews(cfg, true) {
		if filter.keeps(view) {
			hidden++
		}
	}
	switch hidden {
	case 0:
		return ""
	case 1:
		return "1 archived project is hidden; --archived lists it.\n"
	}
	return fmt.Sprintf("%d archived projects are hidden; --archived lists them.\n", hidden)
}

// writeProjectLines is `project list` off a terminal: headerless and
// tab-separated, as `session list` and `worktree list` are for pipes, with the
// directory cleaned of a configured trailing slash, so it compares equal to
// $PWD in the project, and its problem in a field of its own rather than
// appended to the path, so `cut -f4` yields a path. An empty list stays empty,
// since a script reading rows must not mistake a sentence for a project.
func writeProjectLines(out io.Writer, views []projectView) {
	for _, view := range views {
		problem := ""
		if view.DirectoryProblem != "" {
			problem = projectProblemLabel(view.DirectoryProblem)
		}
		directory := view.Directory
		if directory != "" {
			directory = filepath.Clean(directory)
		}
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n", view.Profile, view.Title, view.Harness, directory, groupsField(view.Groups), problem)
	}
}

// groupsField is a project's groups as one text field, sorted, so two projects
// declaring the same groups in a different order read, grep, and sort alike.
// JSON keeps them in configured order.
func groupsField(groups []string) string {
	return strings.Join(slices.Sorted(slices.Values(groups)), ",")
}

// projectDirectoryColumn is the DIRECTORY column's index in the table, and
// projectDirectoryMinWidth the narrowest it is cut to: past that the row wraps
// instead, because a path cut to a few cells names nothing.
const (
	projectDirectoryColumn   = 3
	projectDirectoryMinWidth = 24
)

// fitProjectDirectories cuts DIRECTORY cells from the left when the table is
// wider than a terminal width cells wide, keeping the end of each path, which
// is the part that tells projects apart, and any "(missing)" mark after it.
// A width of zero is a terminal that would not say, and nothing is cut.
func fitProjectDirectories(header []string, rows [][]string, width int) {
	if width <= 0 {
		return
	}
	total := 0
	directory := 0
	for i := range header {
		column := layout.Width(header[i])
		for _, row := range rows {
			column = max(column, layout.Width(row[i]))
		}
		if i == projectDirectoryColumn {
			directory = column
		}
		total += column
		if i < len(header)-1 {
			total += 2
		}
	}
	if total <= width {
		return
	}
	limit := max(projectDirectoryMinWidth, directory-(total-width))
	for _, row := range rows {
		row[projectDirectoryColumn] = layout.TruncateLeft(row[projectDirectoryColumn], limit, "…")
	}
}

// projectListFilter is what --profile, --harness, and --group keep. Values of
// one flag are alternatives and the flags combine, so `--profile a --profile b
// --group web` keeps the web projects of either profile. Names compare
// without regard to case, as a profile lookup does.
type projectListFilter struct {
	profiles, harnesses, groups []string
}

// newProjectListFilter refuses a name the configuration does not know rather
// than matching it against nothing, since an empty table would read as "no
// such projects" instead of "no such name" — the same rule `session list`
// applies to its selectors.
func newProjectListFilter(cfg *config.Config) (projectListFilter, error) {
	var filter projectListFilter
	for _, name := range projectListProfiles {
		if !slices.ContainsFunc(cfg.Profiles, func(profile config.Account) bool { return strings.EqualFold(profile.Name, name) }) {
			return filter, cli.UnknownProfile(cfg, name)
		}
		filter.profiles = append(filter.profiles, name)
	}
	for _, name := range projectListHarnesses {
		known := harness.NormalizeKnown(name)
		if known == "" {
			return filter, fmt.Errorf("--harness %q: want one of %s", name, strings.Join(harness.Names(), ", "))
		}
		filter.harnesses = append(filter.harnesses, known)
	}
	configured := projectGroups(cfg)
	for _, name := range projectListGroups {
		if !slices.ContainsFunc(configured, func(group string) bool { return strings.EqualFold(group, name) }) {
			if len(configured) == 0 {
				return filter, fmt.Errorf("--group %q: no project declares a group", name)
			}
			return filter, fmt.Errorf("--group %q: want one of %s", name, strings.Join(configured, ", "))
		}
		filter.groups = append(filter.groups, name)
	}
	return filter, nil
}

func (f projectListFilter) active() bool {
	return len(f.profiles)+len(f.harnesses)+len(f.groups) > 0
}

func (f projectListFilter) keeps(view projectView) bool {
	matches := func(wanted []string, have ...string) bool {
		if len(wanted) == 0 {
			return true
		}
		for _, w := range wanted {
			if slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, w) }) {
				return true
			}
		}
		return false
	}
	return matches(f.profiles, view.Profile) && matches(f.harnesses, view.Harness) && matches(f.groups, view.Groups...)
}

// projectGroups is every group a configured project declares, sorted and
// without duplicates.
func projectGroups(cfg *config.Config) []string {
	var groups []string
	for _, project := range cfg.Projects {
		for _, group := range project.Groups {
			if !slices.Contains(groups, group) {
				groups = append(groups, group)
			}
		}
	}
	sort.Strings(groups)
	return groups
}

// projectProblemLabel shortens a directory problem to fit a table cell; the
// detail after the colon of "cannot be inspected: ..." is in --json and in
// `amx config validate`.
func projectProblemLabel(problem string) string {
	switch {
	case problem == "does not exist":
		return "missing"
	case strings.HasPrefix(problem, "cannot be inspected"):
		return "unreadable"
	default:
		return problem
	}
}

// projectViews is every project that is not archived, or, with archived,
// every project that is.
func projectViews(cfg *config.Config, archived bool) []projectView {
	views := make([]projectView, 0, len(cfg.Projects))
	for _, project := range cfg.Projects {
		if project.Archived == archived {
			views = append(views, projectViewOf(cfg, project))
		}
	}
	return views
}

func projectViewOf(cfg *config.Config, project config.Session) projectView {
	view := projectView{
		Profile:   project.EffectiveProfile(),
		Title:     project.Title,
		Directory: project.Directory,
		Groups:    append([]string{}, project.Groups...),
		Archived:  project.Archived,
	}
	for _, profile := range cfg.Profiles {
		if strings.EqualFold(profile.Name, view.Profile) {
			view.Harness = profile.EffectiveHarness()
			break
		}
	}
	return view
}
