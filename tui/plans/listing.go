package plans

import planindex "github.com/sglavoie/agentmux/internal/plans"

// sortLabel returns the short label rendered in the Plans header to signal
// which ordering is active. Kept distinct from the SortMode constants so the
// on-screen labels can evolve without touching the internal type.
func sortLabel(mode planindex.SortMode) string {
	switch mode {
	case planindex.SortByModTime:
		return "recent"
	default:
		return "matches"
	}
}

// NextSortMode advances mode through the cycle:
// SortByMatches → SortByModTime → SortByMatches.
func NextSortMode(mode planindex.SortMode) planindex.SortMode {
	switch mode {
	case planindex.SortByMatches:
		return planindex.SortByModTime
	default:
		return planindex.SortByMatches
	}
}

// ProjectLabel returns the project a plan belongs to for display in the Plans
// list and viewer header. A plan whose metadata names no project falls back to
// its first discovered project name, then to "Unknown", so a row never renders
// an empty column.
func ProjectLabel(meta planindex.PlanMeta) string {
	if meta.Project != "" {
		return meta.Project
	}
	if projects := meta.ProjectNames(); len(projects) > 0 {
		return projects[0]
	}
	return "Unknown"
}
