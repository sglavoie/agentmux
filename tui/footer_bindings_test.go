package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestFooterBindingStyles(t *testing.T) {
	available := renderFooterBinding("enter", "apply")
	if !strings.Contains(available, styleFooterKey.Render("enter")) {
		t.Fatalf("available binding does not use the accent key style: %q", available)
	}
	if !strings.Contains(available, styleFooter.Render("apply")) {
		t.Fatalf("available binding does not retain the dim label style: %q", available)
	}

	unavailable := renderUnavailableFooterBinding("x", "clear")
	if !strings.Contains(unavailable, styleFooterUnavailableKey.Render("x")) {
		t.Fatalf("unavailable binding does not use the bold muted key style: %q", unavailable)
	}
	if !strings.Contains(unavailable, styleFooter.Render("clear")) {
		t.Fatalf("unavailable binding label is not dim: %q", unavailable)
	}

	destructive := renderFooterBindingWithLabel("[f]", " ", "also delete file", styleError)
	if !strings.Contains(destructive, styleFooterKey.Render("[f]")) ||
		!strings.Contains(destructive, styleError.Render("also delete file")) {
		t.Fatalf("destructive binding did not split accent key from red label: %q", destructive)
	}

	for name, rendered := range map[string]string{
		"available":   available,
		"unavailable": unavailable,
		"destructive": destructive,
	} {
		if got, want := layout.StyledWidth(rendered), layout.Width(stripAnsi(rendered)); got != want {
			t.Fatalf("%s binding width = %d, want visible width %d", name, got, want)
		}
	}
}

func TestRootFooterBindingsRemoveSeparatorColonsAndPreserveSemanticColons(t *testing.T) {
	search := stripAnsi(renderFooter(Model{mode: ModeSearch, width: 200}))
	if !strings.Contains(search, "filter: @profile") {
		t.Fatalf("search footer lost DSL colon: %q", search)
	}
	for _, forbidden := range []string{"enter:", "esc:"} {
		if strings.Contains(search, forbidden) {
			t.Fatalf("search footer retained key/description colon %q: %q", forbidden, search)
		}
	}

	picker := stripAnsi(renderFooter(Model{mode: ModePicker, width: 300}))
	if !strings.Contains(picker, "DSL: @profile, word") {
		t.Fatalf("picker footer lost syntax colon: %q", picker)
	}
	for _, forbidden := range []string{"space:", "enter:", "j/k:", "g/G:"} {
		if strings.Contains(picker, forbidden) {
			t.Fatalf("picker footer retained key/description colon %q: %q", forbidden, picker)
		}
	}

	list := stripAnsi(renderFooter(Model{mode: ModeList, width: 400}))
	if !strings.Contains(list, ": commands") {
		t.Fatalf("list footer lost literal command-palette key: %q", list)
	}
	if strings.Contains(list, "j/k:") || strings.Contains(list, "enter:") {
		t.Fatalf("list footer retained navigation key/description colon: %q", list)
	}
	// `jump next: p)perm` was the one semantic colon the dashboard footer had.
	// It went with the chord, so the dashboard footer now has no semantic colon
	// at all beyond the palette key's own literal spelling, which the assertion
	// above already covers. The search and picker footers still carry theirs.
}

func TestFooterCompactPromptsAndTruncatedMoreBinding(t *testing.T) {
	confirm := stripAnsi(renderFooter(Model{mode: ModeConfirm, width: 80}))
	for _, want := range []string{"[y]es", "[n]o", "[esc] cancel"} {
		if !strings.Contains(confirm, want) {
			t.Fatalf("confirm footer = %q, want compact prompt %q", confirm, want)
		}
	}

	raw := strings.Join(footerHintLines(Model{width: 20}, hintTestPalette, 1), "\n")
	if !strings.Contains(stripAnsi(raw), footerHintMore) {
		t.Fatalf("truncated footer lost more binding: %q", stripAnsi(raw))
	}
	if !strings.Contains(raw, styleFooterKey.Render("?")) {
		t.Fatalf("truncated footer more key is not accented: %q", raw)
	}
}

func TestModalAndStandaloneHintRowsUseFooterBindings(t *testing.T) {
	target := transcript.SessionMeta{ID: "abc", Project: "demo"}
	cases := []struct {
		name string
		view string
		key  string
	}{
		{name: "actions", view: renderActions(Model{}, 46), key: "a key"},
		{name: "command palette", view: renderCommandPalette(commandPaletteTestModel(100, 40)), key: "enter"},
		{name: "detail", view: detailFooter(Model{}, 10, 10), key: "t"},
		{name: "timeline", view: renderTimeline(Model{width: 100, height: 30}), key: "t"},
		{name: "delete choice", view: renderDeleteChoice(Model{deleteTarget: &target}), key: "[i]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.view, styleFooterKey.Render(tc.key)) {
				t.Fatalf("hint row does not accent key %q:\n%s", tc.key, tc.view)
			}
		})
	}
}
