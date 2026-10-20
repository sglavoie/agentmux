package tui

// This test loads the committed example scenarios documented under
// docs/tui-testing/ and runs them through the same parser and package-level
// runner the hidden CLI uses. It exists so the documentation examples can never
// drift from the harness: a scenario that stops parsing, references a renamed
// fixture, or regresses a layout guarantee fails here in-package without
// shelling out to the CLI.

import (
	"os"
	"path/filepath"
	"testing"
)

// exampleScenariosDir is the committed scenario directory relative to this
// package (tui/ -> repo root -> docs/tui-testing/scenarios).
const exampleScenariosDir = "../docs/tui-testing/scenarios"

// TestExampleScenariosParseAndPass loads every committed example scenario,
// confirms it parses under the same validation the CLI uses, and runs it to a
// passing result. All committed examples are fixture-backed, so no config
// staging environment is needed.
func TestExampleScenariosParseAndPass(t *testing.T) {
	entries, err := os.ReadDir(exampleScenariosDir)
	if err != nil {
		t.Fatalf("reading example scenarios dir: %v", err)
	}

	var jsonFiles []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		jsonFiles = append(jsonFiles, e.Name())
	}
	if len(jsonFiles) == 0 {
		t.Fatalf("no example scenarios found in %s", exampleScenariosDir)
	}

	// Keep this exact inventory in lock-step with docs/tui-testing/README.md.
	// Every committed file is still executed below; rejecting untracked names
	// prevents the documentation and acceptance recipe from silently diverging.
	want := map[string]bool{
		"filter-hint-narrow.json":        false,
		"filter-hint-wide.json":          false,
		"filter-incomplete-project.json": false,
		"setup-profile-narrow.json":      false,
		"setup-profile-wide.json":        false,
		"setup-project-narrow.json":      false,
		"setup-project-wide.json":        false,

		"send-editor-narrow.json":                              false,
		"send-editor-wide.json":                                false,
		"feedback-wide.json":                                   false,
		"feedback-notices-wide.json":                           false,
		"feedback-notices-narrow.json":                         false,
		"feedback-narrow.json":                                 false,
		"project-session-shortcut.json":                        false,
		"actions-menu-disabled-entry-explains.json":            false,
		"actions-menu-initial-selection.json":                  false,
		"actions-menu-nested-confirmation-fit.json":            false,
		"actions-menu-nested-picker-fit.json":                  false,
		"actions-menu-selection-moves.json":                    false,
		"actions-popup-fit.json":                               false,
		"asl-abort-confirmation-fit.json":                      false,
		"asl-runs-abort-key-wide.json":                         false,
		"asl-runs-abort-key-narrow-fit.json":                   false,
		"asl-reply-keeps-draft-wide.json":                      false,
		"asl-reply-keeps-draft-narrow-fit.json":                false,
		"plan-view-header-wide.json":                           false,
		"plan-view-header-narrow-fit.json":                     false,
		"session-detail-help-wide.json":                        false,
		"session-detail-help-narrow-fit.json":                  false,
		"asl-question-pending-narrow-fit.json":                 false,
		"asl-question-pending-wide.json":                       false,
		"asl-response-live-wide.json":                          false,
		"asl-response-paused-narrow-fit.json":                  false,
		"asl-runs-narrow-fit.json":                             false,
		"asl-runs-wide.json":                                   false,
		"attention-overlap-reasons.json":                       false,
		"band-boundary-119-standard.json":                      false,
		"band-boundary-120-spacious.json":                      false,
		"band-boundary-79-compact-fit.json":                    false,
		"band-boundary-80-standard.json":                       false,
		"baseline-layout.json":                                 false,
		"blocked-reason-column-narrow-fit.json":                false,
		"blocked-reason-column-wide.json":                      false,
		"branch-column-narrow-fit.json":                        false,
		"branch-column-unreserved.json":                        false,
		"branch-column-wide.json":                              false,
		"combined-actions-during-refresh.json":                 false,
		"combined-filter-then-resize.json":                     false,
		"combined-folded-attention-reveal.json":                false,
		"combined-preview-read-failure.json":                   false,
		"combined-preview-read-recovery.json":                  false,
		"combined-reorder-while-paused.json":                   false,
		"command-action-by-name.json":                          false,
		"command-palette-execute-help.json":                    false,
		"command-palette-filter.json":                          false,
		"command-palette-narrow-scroll.json":                   false,
		"command-palette-wide.json":                            false,
		"compact-below-split-floor.json":                       false,
		"compact-family-below-split-floor.json":                false,
		"config-picker-narrow-fit.json":                        false,
		"config-picker-fit.json":                               false,
		"config-picker-selection-transition.json":              false,
		"contextual-help-from-session-view.json":               false,
		"duplicate-names-collisions.json":                      false,
		"edge-cold-daemon-after-refresh.json":                  false,
		"edge-filter-matches-nothing.json":                     false,
		"edge-long-metadata-truncation.json":                   false,
		"edge-single-session.json":                             false,
		"edge-zero-sessions.json":                              false,
		"family-unread-collapsed.json":                         false,
		"family-unread-expanded.json":                          false,
		"footer-cap-119-standard.json":                         false,
		"footer-cap-120-spacious.json":                         false,
		"footer-cap-79-compact-fit.json":                       false,
		"footer-cap-80-standard.json":                          false,
		"footer-cap-short-height.json":                         false,
		"footer-filter-chips-active.json":                      false,
		"footer-gloss-narrow-fit.json":                         false,
		"footer-survives-inspector.json":                       false,
		"full-screen-inspector-tab-inert.json":                 false,
		"health-details-overlay-narrow.json":                   false,
		"health-details-overlay-wide.json":                     false,
		"health-states-hooks-degraded.json":                    false,
		"held-inspector-resumes-on-new-selection.json":         false,
		"held-inspector-survives-launcher-preview-toggle.json": false,
		"held-inspector-survives-launcher-preview.json":        false,
		"held-inspector-survives-launcher-session-search.json": false,
		"held-inspector-survives-resize.json":                  false,
		"help-modal-narrow-fit.json":                           false,
		"help-modal-scroll-bottom.json":                        false,
		"launcher-session-search-leaves-no-stale-hold.json":    false,
		"legend-narrow-fit.json":                               false,
		"mouse-chip-removal.json":                              false,
		"mouse-header-controls.json":                           false,
		"mouse-list-wheel.json":                                false,
		"mouse-session-select-focus.json":                      false,
		"mouse-switcher-ignores-wheel.json":                    false,
		"mouse-switcher-scope.json":                            false,
		"needs-you-reveals-folded-target.json":                 false,
		"paused-output-follows-tail.json":                      false,
		"picker-dsl-narrow-fit.json":                           false,
		"picker-fit.json":                                      false,
		"picker-preview-fit.json":                              false,
		"plan-search-header-narrow-fit.json":                   false,
		"plan-search-narrow-fit.json":                          false,
		"plan-run-form-wide.json":                              false,
		"plan-run-refused-narrow-fit.json":                     false,
		"plan-search-sort-rebind.json":                         false,
		"plan-view-fit.json":                                   false,
		"plan-view-narrow-fit.json":                            false,
		"portable-dashboard-narrow-fit.json":                   false,
		"portable-dashboard-wide.json":                         false,
		"portable-launch-picker-narrow-fit.json":               false,
		"portable-launch-picker-wide.json":                     false,
		"portable-open-here-narrow-fit.json":                   false,
		"portable-open-here-wide.json":                         false,
		"portable-resort-always-sorted-wide.json":              false,
		"portable-resort-always-sorted.json":                   false,
		"portable-session-action-names-tmux-narrow-fit.json":   false,
		"portable-session-action-names-tmux-wide.json":         false,
		"project-scope-survives-resize.json":                   false,
		"project-switcher-keyboard-apply.json":                 false,
		"project-switcher-narrow-fit.json":                     false,
		"project-switcher-over-split.json":                     false,
		"projects-cross-view-selection.json":                   false,
		"projects-cross-view-summary-return.json":              false,
		"projects-family-collapse.json":                        false,
		"projects-family-expand.json":                          false,
		"projects-filtered-attention-counts.json":              false,
		"projects-summary-action-refusal.json":                 false,
		"projects-summary-preview.json":                        false,
		"projects-tree-expanded.json":                          false,
		"resize-stress.json":                                   false,
		"round-trip-actions-to-inspector.json":                 false,
		"round-trip-confirmation-cancel-to-inspector.json":     false,
		"round-trip-health-to-family-card.json":                false,
		"round-trip-help-to-family-card.json":                  false,
		"round-trip-history-to-inspector.json":                 false,
		"round-trip-launcher-to-inspector.json":                false,
		"round-trip-plans-to-family-card.json":                 false,
		"round-trip-runs-to-family-card.json":                  false,
		"round-trip-worktrees-to-inspector.json":               false,
		"run-timer-drop-order.json":                            false,
		"run-timer-narrow-fit.json":                            false,
		"run-timer-wide.json":                                  false,
		"search-narrow-fit.json":                               false,
		"session-fork-continuity.json":                         false,
		"session-search-chips-fit.json":                        false,
		"session-search-ending-narrow.json":                    false,
		"session-search-ending-wide.json":                      false,
		"session-search-header-narrow-fit.json":                false,
		"session-view-hints-narrow-fit.json":                   false,
		"session-view-hints-wide.json":                         false,
		"size-100x24-triage.json":                              false,
		"size-120x30-triage.json":                              false,
		"size-160x45-triage.json":                              false,
		"size-60x15-triage.json":                               false,
		"size-80x24-triage.json":                               false,
		"size-extreme-short-window.json":                       false,
		"size-split-floor-100-split.json":                      false,
		"size-split-floor-99-no-split.json":                    false,
		"spacious-identity-columns.json":                       false,
		"triage-contextual-project-completion.json":            false,
		"triage-default-wide-ordering.json":                    false,
		"triage-horizontal-preview.json":                       false,
		"triage-quoted-scope-round-trip.json":                  false,
		"triage-selector-survives-width-change.json":           false,
		"triage-switcher-at-narrow-width.json":                 false,
		"triage-switcher-contextual-counts.json":               false,
		"triage-switcher-scroll.json":                          false,
		"triage-vertical-direction-ignored.json":               false,
		"triage-view-round-trip.json":                          false,
		"unseen-stop-grouping-narrow-fit.json":                 false,
		"unseen-stop-grouping-wide.json":                       false,
		"worktrees-browse.json":                                false,
		"worktrees-cleanup-active-refusal.json":                false,
		"worktrees-cleanup-dirty-refusal.json":                 false,
		"worktrees-cleanup-looping-refusal.json":               false,
		"worktrees-delete-merged-branch.json":                  false,
		"worktrees-delete-unmerged-refusal.json":               false,
		"worktrees-diff-missing-refusal.json":                  false,
		"worktrees-forget-invalid.json":                        false,
		"worktrees-forget-missing.json":                        false,
		"worktrees-land-active-refusal.json":                   false,
		"worktrees-land-confirm-narrow-fit.json":               false,
		"worktrees-land-diverged-refusal.json":                 false,
		"worktrees-land-fast-forward.json":                     false,
		"worktrees-partial-recovery.json":                      false,
		"worktrees-picker-narrow-fit.json":                     false,
		"worktrees-remove-retain-branch.json":                  false,
		"worktrees-reopen-ambiguous.json":                      false,
		"worktrees-reopen-fresh.json":                          false,
		"worktrees-reopen-live.json":                           false,
		"worktrees-reopen-replacement-profile.json":            false,
		"worktrees-reopen-resume.json":                         false,
		"worktrees-short-layout.json":                          false,
	}

	for _, name := range jsonFiles {
		path := filepath.Join(exampleScenariosDir, name)
		sc, err := LoadUITestScenario(path)
		if err != nil {
			t.Errorf("%s: load/parse failed: %v", name, err)
			continue
		}
		if sc.Config != "" {
			t.Errorf("%s: committed examples must be fixture-backed, got config %q", name, sc.Config)
			continue
		}
		if _, tracked := want[name]; !tracked {
			t.Errorf("undocumented scenario %s is not in the exact acceptance inventory", name)
		} else {
			want[name] = true
		}
		res, err := RunUITestScenario(sc, UITestScenarioEnv{})
		if err != nil {
			t.Errorf("%s: run failed: %v", name, err)
			continue
		}
		if !res.OK {
			t.Errorf("%s: scenario did not pass: %+v", name, res.Failures)
		}
	}

	for name, seen := range want {
		if !seen {
			t.Errorf("documented acceptance scenario %s is missing", name)
		}
	}
}
