package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
)

// modelPlanState holds the plan browser and viewer state.
// Embedded by value so Bubble Tea updates keep the model copy semantics.
type modelPlanState struct {
	// plan search view (ModePlanSearch). plansIndex is lazy-constructed on first
	// entry and reused across refreshes. planResults holds the most recent
	// plans.Search output; planQuery is the text typed after `/`. planSearchMode
	// is true while editing planQuery. planSearchSeq dedupes in-flight
	// refresh/search dispatches the same way sessionSearchSeq does.
	plansIndex            *plans.Index
	planCacheLoadInFlight bool
	planRefreshCommand    func(*plans.Index, *config.Config, int) tea.Cmd
	planRunCommand        func(int, plans.PlanMeta, map[string]string) tea.Cmd
	planSearchCommand     func(int, *plans.Index, plans.Query) tea.Cmd
	planQuery             string
	planResults           []plans.Result
	planCursor            int
	planOffset            int
	planSearchMode        bool
	planLoading           bool
	planRefreshErr        error
	planSearchErr         error
	planSearchSeq         int
	// planScanSeq is the search seq of the source refresh still running, zero
	// once it reported; the "Scanning plans…" notice is settled only after it.
	planScanSeq int
	// The query as it was when `/` opened the prompt: Esc restores it,
	// while Enter keeps what was typed.
	planQuerySaved    string
	planSortMode      plans.SortMode // cycled with `o`; zero value = SortByMatches
	planFilterProject plans.ProjectFilter

	// plan viewer (ModePlanView). viewerPlan points at the plan meta the
	// user opened with Enter; nil when the viewer is closed. planViewerLines
	// is the flattened concatenation of every file in viewerPlan.Files with
	// "--- <rel> ---" separator rows between them. planViewerCursor is a
	// logical source-line index into planViewerLines; planViewerScroll is a
	// visual wrapped-row offset derived from those logical lines. The viewer
	// keeps selection on source lines so actions like `y` copy the raw line,
	// while windowing and page keys operate on wrapped terminal rows.
	// planViewerMatchLines holds the logical line indices containing planQuery
	// (case-insensitive substring); planViewerMatchIdx is the cursor into that
	// list for n/N navigation. planViewerLoadSeq is monotonic so a stale async
	// load (user already left the viewer or opened a different plan) is
	// dropped in Update.
	viewerPlan           *plans.PlanMeta
	planViewerLines      []string
	planViewerCursor     int
	planViewerScroll     int
	planViewerMatchIdx   int
	planViewerMatchLines []int
	planViewerLoading    bool
	planViewerLoadErr    error
	planViewerLoadSeq    int
}
