package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenWidthSelector names one of the raw-width identifiers the
// Sessions 1-7 migration banned from production code in tui/.
type forbiddenWidthSelector struct {
	pkgImport string // canonical import path
	name      string // selector name (e.g. "StringWidth")
}

// forbiddenWidthRules lists every production-code raw width API banned
// by the centralized helper policy. Test files and tui/display_width.go
// are exempt; see TestSourceAuditNoRawWidthAPIsOutsideHelper.
var forbiddenWidthRules = []forbiddenWidthSelector{
	{pkgImport: "github.com/mattn/go-runewidth", name: "StringWidth"},
	{pkgImport: "github.com/mattn/go-runewidth", name: "Truncate"},
	{pkgImport: "charm.land/lipgloss/v2", name: "Width"},
}

// widthAuditViolation records a single forbidden-API hit found during
// the AST walk. Returned in source order so error messages stay
// deterministic.
type widthAuditViolation struct {
	File string
	Line int
	Expr string
}

// auditFileForRawWidthAPIs parses src as Go source and returns every
// forbidden selector usage (runewidth.StringWidth / runewidth.Truncate
// / lipgloss.Width). The walker honors import aliases — both renamed
// imports (`runewidth "github.com/mattn/go-runewidth"`) and default
// aliases derived from the canonical path — so the policy enforces by
// import identity, not by surface text.
func auditFileForRawWidthAPIs(fset *token.FileSet, filename string, src []byte) ([]widthAuditViolation, error) {
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	aliasToPath := map[string]string{}
	for _, imp := range file.Imports {
		canonical := strings.Trim(imp.Path.Value, `"`)
		var alias string
		switch {
		case imp.Name != nil && imp.Name.Name != "" && imp.Name.Name != "_" && imp.Name.Name != ".":
			alias = imp.Name.Name
		default:
			alias = filepath.Base(canonical)
			if strings.HasPrefix(alias, "v") {
				trimmed := strings.TrimLeft(alias, "v")
				if trimmed != "" && trimmed != alias && allDigits(trimmed) {
					parts := strings.Split(canonical, "/")
					if len(parts) >= 2 {
						alias = parts[len(parts)-2]
					}
				}
			}
		}
		aliasToPath[alias] = canonical
	}

	var hits []widthAuditViolation
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		path, ok := aliasToPath[x.Name]
		if !ok {
			return true
		}
		for _, rule := range forbiddenWidthRules {
			if path == rule.pkgImport && sel.Sel.Name == rule.name {
				pos := fset.Position(sel.Pos())
				hits = append(hits, widthAuditViolation{
					File: pos.Filename,
					Line: pos.Line,
					Expr: x.Name + "." + sel.Sel.Name,
				})
			}
		}
		return true
	})
	return hits, nil
}

// TestSourceAuditNoRawWidthAPIsOutsideHelper enforces the repo-wide TUI
// width policy established by Sessions 1-7: production code under tui/
// must route every visible-cell measurement, truncation, padding, and
// centering call through the helpers in tui/display_width.go.
//
// The audit walks every non-test .go file in the tui/ package and uses
// the Go AST to detect direct identifier references to
// runewidth.StringWidth, runewidth.Truncate, or lipgloss.Width. A regex
// over file text would also flag occurrences inside comments and
// strings; the AST walk only flags actual selector targets so the
// policy stays precise.
//
// Exemptions (intentional):
//   - tui/display_width.go is the helper module that owns the raw API.
//   - *_test.go files use these primitives as independent measurement
//     assertions and are explicitly allowed by Session 7's handoff.
//
// Lip Gloss style/layout APIs such as lipgloss.NewStyle().Width(...),
// lipgloss.JoinHorizontal, lipgloss.JoinVertical, lipgloss.Wrap, and
// lipgloss.Color are not in scope: only the three measurement
// identifiers above are forbidden in production code.
func TestSourceAuditNoRawWidthAPIsOutsideHelper(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read tui dir: %v", err)
	}

	fset := token.NewFileSet()
	var checked int
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "display_width.go" {
			continue
		}

		checked++
		path := filepath.Join(".", name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		hits, err := auditFileForRawWidthAPIs(fset, path, src)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, h := range hits {
			t.Errorf("%s:%d: forbidden raw-width API %s — route through helpers in display_width.go",
				h.File, h.Line, h.Expr)
		}
	}

	if checked == 0 {
		t.Fatalf("source audit walked zero production files; package layout changed?")
	}
}

// TestSourceAuditDetectsViolationsInSyntheticFixture is a self-test that
// prevents the audit from silently regressing into a no-op. It feeds an
// in-memory file that uses every banned identifier (under both renamed
// and default-alias imports) and asserts every one is flagged.
func TestSourceAuditDetectsViolationsInSyntheticFixture(t *testing.T) {
	t.Parallel()

	const src = `package fake

import (
	"charm.land/lipgloss/v2"
	rw "github.com/mattn/go-runewidth"
)

func bad(s string) int {
	_ = rw.StringWidth(s)
	_ = rw.Truncate(s, 10, "")
	_ = lipgloss.Width(s)
	return 0
}
`
	fset := token.NewFileSet()
	hits, err := auditFileForRawWidthAPIs(fset, "synthetic.go", []byte(src))
	if err != nil {
		t.Fatalf("parse synthetic fixture: %v", err)
	}
	wantExprs := map[string]bool{
		"rw.StringWidth": false,
		"rw.Truncate":    false,
		"lipgloss.Width": false,
	}
	for _, h := range hits {
		if _, ok := wantExprs[h.Expr]; ok {
			wantExprs[h.Expr] = true
			continue
		}
		t.Errorf("unexpected violation reported: %s at line %d", h.Expr, h.Line)
	}
	for expr, seen := range wantExprs {
		if !seen {
			t.Errorf("audit failed to flag %s in synthetic fixture", expr)
		}
	}
}

// TestSourceAuditIgnoresAllowedLipglossStyleBuilders verifies the audit
// does not flag the explicitly-whitelisted Lip Gloss APIs: style
// builders like NewStyle().Width(...), and layout joiners like
// JoinHorizontal / JoinVertical / Wrap. Only the bare lipgloss.Width
// selector is forbidden.
func TestSourceAuditIgnoresAllowedLipglossStyleBuilders(t *testing.T) {
	t.Parallel()

	const src = `package fake

import "charm.land/lipgloss/v2"

func ok(s string) string {
	style := lipgloss.NewStyle().Width(40).Render(s)
	joined := lipgloss.JoinHorizontal(lipgloss.Top, style, s)
	wrapped := lipgloss.Wrap(joined, 60)
	return lipgloss.JoinVertical(lipgloss.Top, wrapped)
}
`
	fset := token.NewFileSet()
	hits, err := auditFileForRawWidthAPIs(fset, "synthetic_ok.go", []byte(src))
	if err != nil {
		t.Fatalf("parse synthetic fixture: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected zero violations for style-builder fixture; got %+v", hits)
	}
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
