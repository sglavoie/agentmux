package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDomainServicesIssueNoTerminalCommands keeps the session and action
// domain services behind internal/backend: they may still read Kitty's
// topology until later migration sessions replace that, but every terminal
// command (launch, close, focus, title, colors, metadata, capture, reorder)
// goes through a backend.Backend, so no Kitty match expression or `kitten @`
// method is spelled in their non-test sources.
func TestDomainServicesIssueNoTerminalCommands(t *testing.T) {
	forbiddenKitty := map[string]bool{
		"MatchTabByID": true, "MatchTabByTitle": true, "MatchWindowByVar": true,
		"MatchAccountWindows": true, "MatchWindowByID": true, "MatchTabByWindowID": true,
		"LaunchOpts": true, "TabColorDefault": true,
	}
	commandMethods := map[string]bool{
		"LaunchTab": true, "LaunchOverlay": true, "CloseTab": true, "MoveTab": true,
		"SetTabColor": true, "SetTabTitle": true, "SetUserVars": true, "FocusTab": true,
		"FocusWindow": true, "GetText": true, "LaunchOverlayPager": true,
	}
	for _, dir := range []string{"internal/session", "internal/actions"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no Go files in %s", dir)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					if pkg, ok := node.X.(*ast.Ident); ok && pkg.Name == "kitty" && forbiddenKitty[node.Sel.Name] {
						t.Errorf("%s: domain code uses kitty.%s; go through internal/backend", fset.Position(node.Pos()), node.Sel.Name)
					}
				case *ast.CallExpr:
					if sel, ok := node.Fun.(*ast.SelectorExpr); ok && commandMethods[sel.Sel.Name] {
						t.Errorf("%s: domain code calls %s; go through internal/backend", fset.Position(node.Pos()), sel.Sel.Name)
					}
				case *ast.BasicLit:
					if node.Kind == token.STRING {
						if s, err := strconv.Unquote(node.Value); err == nil && s == "kitten" {
							t.Errorf("%s: domain code names the kitten binary", fset.Position(node.Pos()))
						}
					}
				}
				return true
			})
		}
	}
}
