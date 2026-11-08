package overview

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The package has three halves — the arithmetic is the point — and each is held
// to its own structural rule, so every file has to be classified as exactly one.
// The lists below are the classification, and parsePackage fails on a file that
// is in none, which is what makes adding a file a deliberate act rather than a
// way to opt out of every allowlist.
//
// renderingFiles are pure: a session slice, a preview map and a supplied Now go
// in, and a string comes out.
//
// lifecycleFiles are the running program's OBSERVING half: they start commands,
// read the clock, and hold the state the renderer is handed. Their allowlist is
// the one that pins "browsing the overview cannot change anything" — every
// navigation key, every refresh, and every capture lands in one of these files,
// and none of them may name a command that writes.
//
// actionFiles are the running program's ACTING half, and splitting them out is
// what keeps that claim assertable now that the grid offers the thirteen session
// actions. The mutations exist, but they exist in the classified action files, they are
// all reached through a dispatched action, and the observing half still cannot
// name one of them — which is a sharper statement than the single lifecycle
// allowlist could make once it had to admit commands.Close.
//
// The keymap is split along the same line. bindings.go is the lifecycle's,
// because a table whose entries are dispatchable runners is the routing; help.go
// is the renderer's, because the overlay it draws is a geometry struct in and a
// string out; and help_keys.go, which holds the state the two share, is where the
// keys that move it live.
var (
	renderingFiles = []string{"chrome.go", "footer.go", "grid.go", "help.go", "nav.go", "order.go", "view.go"}
	lifecycleFiles = []string{"bindings.go", "capture.go", "help_keys.go", "model.go", "preferences.go", "refresh.go", "status.go", "update.go"}
	actionFiles    = []string{"action_keys.go", "actions.go", "action_commands.go", "action_reports.go", "action_surfaces.go", "recovery.go"}
)

// parsePackage parses every non-test file in the package and returns them keyed
// by name, having first checked that the classification above accounts for all of
// them exactly once.
//
// Files are parsed in full rather than imports-only because the callers below
// walk function bodies.
func parsePackage(t *testing.T) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	onDisk := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		onDisk[name] = true
	}
	if len(onDisk) == 0 {
		t.Fatal("no non-test files were found; every structural assertion below would pass vacuously")
	}

	classified := map[string]bool{}
	all := append([]string{}, renderingFiles...)
	all = append(all, lifecycleFiles...)
	all = append(all, actionFiles...)
	for _, name := range all {
		if classified[name] {
			t.Errorf("%s is classified twice; it would be held to two rules and to none exclusively", name)
		}
		classified[name] = true
		if !onDisk[name] {
			t.Errorf("%s is classified but is not in the package", name)
		}
	}
	for name := range onDisk {
		if !classified[name] {
			t.Errorf("%s is in none of renderingFiles, lifecycleFiles, or actionFiles; classify it so it is held to one of the three structural rules", name)
		}
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(onDisk))
	for name := range onDisk {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files[name] = file
	}
	return files
}

// assertImports checks one file's imports against an allowlist.
func assertImports(t *testing.T, name string, file *ast.File, allowed map[string]bool, rule string) {
	t.Helper()
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("%s: unquoting %s: %v", name, imp.Path.Value, err)
		}
		if !allowed[path] {
			t.Errorf("%s imports %q, which is not on the %s allowlist", name, path, rule)
		}
	}
}

// selectorCalls reports every `pkg.Func(...)` call in a file as "pkg.Func".
func selectorCalls(file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok {
				out = append(out, ident.Name+"."+sel.Sel.Name)
			}
		}
		return true
	})
	return out
}

// TestTheRenderingHalfPerformsNoIO is structural rather than behavioral: a
// renderer that read a file would still return a string, and the only way to be
// sure is to look at what it is allowed to import. Anything that could reach a
// disk, a socket, a subprocess, or a clock is absent by construction — Now is a
// field on Input precisely so this list can stay short.
func TestTheRenderingHalfPerformsNoIO(t *testing.T) {
	allowed := map[string]bool{
		"fmt":     true, // Sprintf into the title row
		"sort":    true,
		"strings": true,
		"time":    true, // time.Duration and time.Time values only; see below

		"charm.land/lipgloss/v2": true,

		"github.com/sglavoie/agentmux/internal/config":  true,
		"github.com/sglavoie/agentmux/internal/session": true,
		"github.com/sglavoie/agentmux/tui/chrome":       true, // footer binding styles only
		"github.com/sglavoie/agentmux/tui/dashboard":    true,
		"github.com/sglavoie/agentmux/tui/layout":       true,
		"github.com/sglavoie/agentmux/tui/theme":        true,
	}

	files := parsePackage(t)
	for _, name := range renderingFiles {
		file, ok := files[name]
		if !ok {
			continue // parsePackage already reported the missing file
		}
		assertImports(t, name, file, allowed, "no-I/O")

		// time is on the list for its types. A call into it would read the
		// machine's clock, which is exactly what Input.Now exists to avoid.
		for _, call := range selectorCalls(file) {
			if call == "time.Now" || call == "time.Since" {
				t.Errorf("%s calls %s; the renderer's clock is Input.Now", name, call)
			}
		}
	}
}

// TestTheLifecycleHalfReachesTheWorldOnlyThroughItsSeams is the running half's
// structural rule, and it is the one that pins "browsing the overview cannot
// change anything".
//
// Two claims, both checked by absence. First, the imports: the lifecycle may name
// the session and config vocabularies, Bubble Tea, the shared commands, and the
// daemon's protocol constants — and nothing that could read a transcript, a plan,
// a socket or a file of its own. Second, the calls into tui/commands: only the
// topology and preview reads, Focus, the view preference, and their tickers. Every
// other command in that package either changes a terminal or asks the daemon for
// something this surface has no business asking, so the allowlist being short is
// the whole assertion. A navigation key cannot change a terminal because there is no
// call in the package it could reach it through.
func TestTheLifecycleHalfReachesTheWorldOnlyThroughItsSeams(t *testing.T) {
	allowedImports := map[string]bool{
		"fmt":     true,
		"strings": true,
		"time":    true,

		"charm.land/bubbletea/v2": true,

		"github.com/sglavoie/agentmux/internal/config":          true,
		"github.com/sglavoie/agentmux/internal/daemon/protocol": true, // TopologyStaleAfter
		"github.com/sglavoie/agentmux/internal/session":         true,
		"github.com/sglavoie/agentmux/tui/commands":             true,
		"github.com/sglavoie/agentmux/tui/dashboard":            true, // shared NeedsYou predicate
		"github.com/sglavoie/agentmux/tui/theme":                true, // terminal appearance only
		// The session-action vocabulary is types only on this side: the two
		// action seams' signatures and the actionState field. Naming it here does
		// not weaken the rule below, which is about the CALLS a file makes.
		"github.com/sglavoie/agentmux/tui/sessionactions": true,
	}
	allowedCommands := map[string]bool{
		"commands.RecoverClosed":       true, // recovery through the same continuity registry
		"commands.OverviewPreferences": true, // only c changes this daemon-lifetime UI setting
		"commands.RefreshOverview":     true,
		"commands.Focus":               true,
		"commands.Tick":                true,
		// The capture is a READ of a pane's terminal text and its cadence. It is
		// listed beside RefreshOverview rather than beside Focus for that reason:
		// commands.CaptureScreen reads through the pane's backend, which observes a pane
		// without touching it, so adding it leaves the "browsing the overview
		// cannot change anything" claim exactly where it was.
		"commands.CaptureScreen": true,
		"commands.CaptureTick":   true,
		// The tmux capture source builds a value
		// and reaches nothing.
		"commands.OverviewCaptureSources": true,
	}

	files := parsePackage(t)
	clockCallers := map[string]bool{}
	for _, name := range lifecycleFiles {
		file, ok := files[name]
		if !ok {
			continue
		}
		assertImports(t, name, file, allowedImports, "lifecycle")

		for _, call := range selectorCalls(file) {
			if strings.HasPrefix(call, "commands.") && !allowedCommands[call] {
				t.Errorf("%s calls %s; the overview observes, and its only mutation is commands.Focus", name, call)
			}
			if call == "time.Now" || call == "time.Since" {
				clockCallers[name] = true
			}
		}
	}

	// The clock is a seam for the same reason the commands are, so it enters the
	// package in exactly one place: the constructor that binds the seams to the
	// real world. Anywhere else it would be a reading a test cannot pin.
	delete(clockCallers, "model.go")
	for name := range clockCallers {
		t.Errorf("%s calls the machine clock; the lifecycle's clock is the deps.now seam bound in model.go", name)
	}
}

// TestTheActingHalfMutatesOnlyThroughADispatchedAction is the acting half's
// structural rule.
//
// The observing half's rule above says a navigation key cannot change a terminal
// because there is no call it could reach it through. This one says where those
// calls ARE, and it is deliberately an allowlist rather than a permission: every
// entry is an effect that some session action dispatches, and a command appearing
// here that no definition names would be a mutation the grid can perform outside
// the controller — which is precisely the thing the shared controller exists to
// make impossible.
//
// The clock rule is the same as the lifecycle's and matters more here, because
// two of the things this half decides are timed: how long a report stays on the
// feedback row, and how long a close stays undoable. Both read deps.now, so an
// acceptance sequence can advance a frozen clock past a thirty-second recovery
// window without sleeping.
func TestTheActingHalfMutatesOnlyThroughADispatchedAction(t *testing.T) {
	allowedImports := map[string]bool{
		"github.com/sglavoie/agentmux/internal/conversation": true, // immutable recovery identity
		"context": true, // the registry's Execute takes one
		"fmt":     true,
		"time":    true,

		"charm.land/bubbletea/v2": true,

		"github.com/sglavoie/agentmux/internal/actions":   true, // the shared typed action registry
		"github.com/sglavoie/agentmux/internal/config":    true,
		"github.com/sglavoie/agentmux/internal/session":   true,
		"github.com/sglavoie/agentmux/tui/commands":       true,
		"github.com/sglavoie/agentmux/tui/dashboard":      true,
		"github.com/sglavoie/agentmux/tui/launch":         true, // the model-free profile picker
		"github.com/sglavoie/agentmux/tui/sessionactions": true,
	}
	// Every entry is dispatched by one of the thirteen menu definitions, plus the
	// stamping and correlation the controller requires and the two lookups that
	// resolve a captured row's configured project.
	allowedCommands := map[string]bool{
		"commands.RecoverClosed":            true, // recovery through the same continuity registry
		"commands.Peek":                     true,
		"commands.Restart":                  true,
		"commands.ClearStatus":              true,
		"commands.CloseSessionRow":          true, // close, through the session's own backend
		"commands.TerminalDependencies":     true, // assembles the terminals an action runs against
		"commands.OpenDirectory":            true,
		"commands.OpenInEditor":             true,
		"commands.CopyTitle":                true,
		"commands.ToggleLock":               true,
		"commands.ToggleMute":               true,
		"commands.MarkActive":               true,
		"commands.StampRequest":             true,
		"commands.FindConfigSession":        true,
		"commands.FindAccountByName":        true,
		"commands.DashboardSessionTemplate": true,
		"commands.SessionWithProfile":       true,
		"commands.RenderActionsBox":         true,
	}

	files := parsePackage(t)
	if len(actionFiles) == 0 {
		t.Fatal("no files are classified as the acting half; every assertion below would pass vacuously")
	}
	for _, name := range actionFiles {
		file, ok := files[name]
		if !ok {
			continue
		}
		assertImports(t, name, file, allowedImports, "acting")

		for _, call := range selectorCalls(file) {
			if strings.HasPrefix(call, "commands.") && !allowedCommands[call] {
				t.Errorf("%s calls %s; every effect here must be one a session action dispatches", name, call)
			}
			if call == "time.Now" || call == "time.Since" {
				t.Errorf("%s calls the machine clock; the acting half's clock is the deps.now seam", name)
			}
		}
	}

	// The registry is reached from here and nowhere else. Two actions need it —
	// restarting under another profile and launching a second session — and the
	// recovery stack's relaunch is the third; a call to it from the observing
	// half would be a mutation outside a dispatch.
	for _, name := range append(append([]string{}, renderingFiles...), lifecycleFiles...) {
		file, ok := files[name]
		if !ok {
			continue
		}
		for _, call := range selectorCalls(file) {
			if strings.HasPrefix(call, "actions.") {
				t.Errorf("%s calls %s; the typed action registry is the acting half's", name, call)
			}
		}
	}
}
