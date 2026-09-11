package dashboard

import (
	"strings"
	"testing"
)

// ---- Components / RemoveComponent ----

func TestFilterComponentsOrder(t *testing.T) {
	f := Parse("api backend #project @work %claude !key")
	comps := f.Components()
	if len(comps) != 6 {
		t.Fatalf("expected 6 components, got %d (%v)", len(comps), comps)
	}
	wantLabels := []string{"/api", "/backend", "#project", "@work", "%claude", "!key"}
	wantKinds := []chipKind{chipSubstring, chipSubstring, chipProject, chipAccount, chipHarness, chipState}
	for i, c := range comps {
		if c.label != wantLabels[i] {
			t.Errorf("comp[%d].label = %q, want %q", i, c.label, wantLabels[i])
		}
		if c.kind != wantKinds[i] {
			t.Errorf("comp[%d].kind = %d, want %d", i, c.kind, wantKinds[i])
		}
	}
}

func TestFilterRemoveProjectPreservesOtherComponents(t *testing.T) {
	f := Parse(`api #"My Project" @work %claude !key`)
	out := f.RemoveComponent(1)
	if out.Project != "" {
		t.Fatalf("expected Project cleared, got %q", out.Project)
	}
	if len(out.Substrings) != 1 || out.Substrings[0] != "api" || out.Account != "work" || out.Harness != "claude" || out.State != "key" {
		t.Fatalf("project removal lost another component: %+v", out)
	}
	if out.Raw != `api @work %claude !key` {
		t.Fatalf("project removal Raw = %q", out.Raw)
	}
}

func TestFilterRemoveSubstringMiddle(t *testing.T) {
	// Remove the second substring out of three components total: substring,
	// substring, account. Result must drop only the targeted substring.
	f := Parse("api backend @work")
	out := f.RemoveComponent(1)
	if !strings.EqualFold(strings.Join(out.Substrings, ","), "api") {
		t.Errorf("expected Substrings=[api], got %v", out.Substrings)
	}
	if out.Account != "work" {
		t.Errorf("expected Account preserved, got %q", out.Account)
	}
	if out.Raw != "api @work" {
		t.Errorf("expected Raw re-serialized to %q, got %q", "api @work", out.Raw)
	}
}

func TestFilterRemoveAccount(t *testing.T) {
	f := Parse("api @work !key")
	out := f.RemoveComponent(1) // account chip
	if out.Account != "" {
		t.Errorf("expected Account cleared, got %q", out.Account)
	}
	if len(out.Substrings) != 1 || out.Substrings[0] != "api" {
		t.Errorf("expected substrings preserved, got %v", out.Substrings)
	}
	if out.State != "key" {
		t.Errorf("expected State preserved, got %q", out.State)
	}
}

func TestFilterRemoveState(t *testing.T) {
	f := Parse("api @work !key")
	out := f.RemoveComponent(2) // state chip
	if out.State != "" {
		t.Errorf("expected State cleared, got %q", out.State)
	}
}

func TestFilterRemoveOutOfRangeNoOp(t *testing.T) {
	f := Parse("api")
	out := f.RemoveComponent(5)
	if len(out.Substrings) != 1 {
		t.Errorf("expected unchanged filter, got %v", out.Substrings)
	}
}

// TestAttentionChipIsColoredLikeTheGroupItSelects keeps the `!attention` chip
// from reading as an ordinary filter token. The chip row is the only place the
// active filter is visible once the search input closes, and a chip naming the
// urgency group has to look like the group's heading — the same style `!key` and
// `!stop`, its two most common members, already get.
func TestAttentionChipIsColoredLikeTheGroupItSelects(t *testing.T) {
	attention := Parse("!attention").Components()
	if len(attention) != 1 {
		t.Fatalf("expected one chip, got %v", attention)
	}
	want := chipStyle(Parse("!key").Components()[0], nil).Render("x")
	if got := chipStyle(attention[0], nil).Render("x"); got != want {
		t.Errorf("!attention chip renders %q, want the attention style %q", got, want)
	}

	// The other two aliases are deliberately NOT special-cased: their groups
	// carry no urgency, so a colored chip would claim one.
	plain := chipStyle(Chip{kind: chipSubstring}, nil).Render("x")
	for _, alias := range []string{"active", "idle"} {
		if got := chipStyle(Parse("!" + alias).Components()[0], nil).Render("x"); got != plain {
			t.Errorf("!%s chip renders %q, want the plain chip style %q", alias, got, plain)
		}
	}
}

func TestSerializeFilterEscapesLeadingMarkers(t *testing.T) {
	f := Filter{Substrings: []string{"@literal", "!banged", "#hashed"}}
	got := SerializeFilter(f)
	if got != `\@literal \!banged \#hashed` {
		t.Errorf("expected escapes, got %q", got)
	}
	// Round-trip must yield the original substrings.
	if rt := Parse(got); rt.Substrings[0] != "@literal" || rt.Substrings[1] != "!banged" || rt.Substrings[2] != "#hashed" || rt.Project != "" {
		t.Errorf("escape round-trip failed: %v", rt.Substrings)
	}
}
