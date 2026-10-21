package actions

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// WorktreeLiveSession is the stable, presentation-neutral subset of a Kitty
// session exposed by worktree list/show and ambiguity errors.
type WorktreeLiveSession struct {
	ID               string `json:"id"`
	TabID            int    `json:"tab_id"`
	WindowID         int    `json:"window_id"`
	Project          string `json:"project,omitempty"`
	Profile          string `json:"profile,omitempty"`
	Harness          string `json:"harness,omitempty"`
	HarnessSessionID string `json:"harness_session_id,omitempty"`
	Directory        string `json:"directory"`
}

// WorktreeView combines Git-authoritative inventory facts with fresh activity
// facts. Fields are intentionally additive under the command schema-v1
// envelope.
type WorktreeView struct {
	worktree.Entry
	LiveSessions []WorktreeLiveSession `json:"live_sessions"`
	LoopCount    int                   `json:"loop_count"`
	Warnings     []string              `json:"warnings"`
}

type usageSnapshot struct {
	live     map[string][]session.RunningSession
	loops    map[string][]agentloop.Candidate
	warnings []string
}

func probeWorktreeUsage(deps Dependencies, entries []worktree.Entry) usageSnapshot {
	snapshot := usageSnapshot{
		live:  make(map[string][]session.RunningSession, len(entries)),
		loops: make(map[string][]agentloop.Candidate, len(entries)),
	}
	liveProbe := deps.LiveSessions
	if liveProbe == nil {
		liveProbe = func(cfg *config.Config) ([]session.RunningSession, error) {
			if deps.Kitty == nil {
				return nil, fmt.Errorf("kitty client is unavailable")
			}
			running, _, err := session.ListWithConfig(deps.Kitty, cfg)
			return running, err
		}
	}
	// A probe that failed may still return what it could read (the tmux
	// sessions when Kitty did not answer). Those are counted; the warning
	// says the count is incomplete, and land and remove refuse on it.
	running, liveErr := liveProbe(deps.Config)
	if liveErr != nil {
		snapshot.warnings = append(snapshot.warnings, "live-session probe: "+liveErr.Error())
	}
	for _, entry := range entries {
		for _, candidate := range running {
			if pathAtOrBelow(entry.Path, candidate.Directory) {
				snapshot.live[entryKey(entry)] = append(snapshot.live[entryKey(entry)], candidate)
			}
		}
		sort.Slice(snapshot.live[entryKey(entry)], func(i, j int) bool {
			return snapshot.live[entryKey(entry)][i].TabID < snapshot.live[entryKey(entry)][j].TabID
		})
	}

	loopProbe := deps.ActiveLoops
	if loopProbe == nil {
		loopProbe = func() ([]agentloop.Candidate, error) {
			return agentloop.Discover(agentloop.DiscoverOptions{})
		}
	}
	loops, loopErr := loopProbe()
	if loopErr != nil {
		snapshot.warnings = append(snapshot.warnings, "active-loop probe: "+loopErr.Error())
	} else {
		for _, entry := range entries {
			for _, candidate := range loops {
				if pathAtOrBelow(entry.Path, candidate.CWD) {
					snapshot.loops[entryKey(entry)] = append(snapshot.loops[entryKey(entry)], candidate)
				}
			}
		}
	}
	return snapshot
}

func worktreeView(entry worktree.Entry, snapshot usageSnapshot) WorktreeView {
	live := snapshot.live[entryKey(entry)]
	loops := snapshot.loops[entryKey(entry)]
	entry.Usage.Live = len(live) > 0
	entry.Usage.Looping = len(loops) > 0
	view := WorktreeView{Entry: entry, LoopCount: len(loops), Warnings: sortedStrings(entry.Warnings)}
	view.LiveSessions = make([]WorktreeLiveSession, 0, len(live))
	for _, running := range live {
		view.LiveSessions = append(view.LiveSessions, liveSessionView(running))
	}
	return view
}

func liveSessionView(value session.RunningSession) WorktreeLiveSession {
	return WorktreeLiveSession{
		ID: strconv.Itoa(value.TabID), TabID: value.TabID, WindowID: value.WindowID,
		Project: value.EffectiveProject(), Profile: value.EffectiveProfile(),
		Harness: value.Harness, HarnessSessionID: value.HarnessSessionID, Directory: value.Directory,
	}
}

func liveSessionCandidates(values []session.RunningSession) []Candidate {
	out := make([]Candidate, 0, len(values))
	for _, value := range values {
		view := liveSessionView(value)
		label := strings.TrimSpace(view.Project)
		if label == "" {
			label = "Kitty tab " + view.ID
		}
		if view.Profile != "" {
			label += " (" + view.Profile + ")"
		}
		out = append(out, Candidate{ID: view.ID, DisplayName: label, CanonicalPath: canonicalComparablePath(view.Directory)})
	}
	return out
}

func loopCandidates(values []agentloop.Candidate) []Candidate {
	out := make([]Candidate, 0, len(values))
	for _, value := range values {
		id := value.RunID
		if id == "" {
			id = value.PlanDir
		}
		out = append(out, Candidate{ID: id, DisplayName: value.PlanDir, CanonicalPath: canonicalComparablePath(value.CWD)})
	}
	return out
}

func pathAtOrBelow(root, candidate string) bool {
	root = canonicalComparablePath(root)
	candidate = canonicalComparablePath(candidate)
	if root == "" || candidate == "" {
		return false
	}
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func canonicalComparablePath(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	abs, err := filepath.Abs(config.ExpandTilde(value))
	if err != nil {
		return ""
	}
	abs = filepath.Clean(abs)
	current := abs
	var suffix []string
	for {
		if canonical, evalErr := filepath.EvalSymlinks(current); evalErr == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				canonical = filepath.Join(canonical, suffix[index])
			}
			return filepath.Clean(canonical)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func entryKey(entry worktree.Entry) string { return entry.ID + "\x00" + entry.Path }
