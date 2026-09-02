package dashboard

import (
	"strings"
	"testing"
)

// ---- Components / RemoveComponent ----

func TestFilterComponentsOrder(t *testing.T) {
	f := Parse("api backend @work !key")
	comps := f.Components()
	if len(comps) != 4 {
		t.Fatalf("expected 4 components, got %d (%v)", len(comps), comps)
	}
	wantLabels := []string{"/api", "/backend", "@work", "!key"}
	wantKinds := []chipKind{chipSubstring, chipSubstring, chipAccount, chipState}
	for i, c := range comps {
		if c.label != wantLabels[i] {
			t.Errorf("comp[%d].label = %q, want %q", i, c.label, wantLabels[i])
		}
		if c.kind != wantKinds[i] {
			t.Errorf("comp[%d].kind = %d, want %d", i, c.kind, wantKinds[i])
		}
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

func TestSerializeFilterEscapesLeadingMarkers(t *testing.T) {
	f := Filter{Substrings: []string{"@literal", "!banged"}}
	got := SerializeFilter(f)
	if got != `\@literal \!banged` {
		t.Errorf("expected escapes, got %q", got)
	}
	// Round-trip must yield the original substrings.
	if rt := Parse(got); rt.Substrings[0] != "@literal" || rt.Substrings[1] != "!banged" {
		t.Errorf("escape round-trip failed: %v", rt.Substrings)
	}
}
