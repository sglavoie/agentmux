package overview

import (
	"strings"
	"testing"
)

// keySpellings is how a key PRINTED on the footer or in the help overlay is read
// back as the key the dispatcher matches.
//
// It is the test's own table on purpose. The two vocabularies are genuinely
// different — an operator reads `PgDn` off their keyboard and Bubble Tea reports
// `pgdown` — and the only way to check that the printed one names the matched
// one is to write the correspondence down and hold every display in the table to
// it. A display with no entry here fails rather than passing unchecked, which is
// what stops a new binding from being advertised in a spelling nothing answers.
var keySpellings = map[string][]string{
	"enter":     {"enter"},
	"↵":         {"enter"},
	"esc":       {"esc"},
	"q":         {"q"},
	"ctrl+c":    {"ctrl+c"},
	"a":         {"a"},
	"?":         {"?"},
	"c":         {"c"},
	"n":         {"n"},
	"N":         {"N"},
	"u":         {"u"},
	"tab":       {"tab"},
	"shift+tab": {"shift+tab"},
	"pgup":      {"pgup"},
	"pgdn":      {"pgdown"},
	"home":      {"home"},
	"end":       {"end"},
	"←↑↓→":      {"left", "up", "down", "right"},
	"hjkl":      {"h", "j", "k", "l"},
	"1–0":       nil, // filled by init from the digit row itself
}

func init() { keySpellings["1–0"] = digitKeys() }

// spelled resolves one printed key group, failing the test for a display the
// table above does not name.
func spelled(t *testing.T, display string) []string {
	t.Helper()
	keys, ok := keySpellings[display]
	if !ok {
		t.Fatalf("no test spelling for the key display %q; add it to keySpellings so what is printed can be checked against what is bound", display)
	}
	return keys
}

// TestNoKeyIsBoundTwice is the dispatcher's own invariant. handleGridKey walks
// the table in declaration order and runs the first binding that matches, so a
// key named by two entries would silently run the earlier one forever and the
// later entry would be dead code that the footer and the help overlay both go on
// advertising.
func TestNoKeyIsBoundTwice(t *testing.T) {
	owner := map[string]string{}
	for _, b := range gridBindings() {
		if len(b.keys) == 0 {
			t.Errorf("the binding %q binds no key at all", b.help)
		}
		for _, k := range b.keys {
			if first, ok := owner[k]; ok {
				t.Errorf("%q is bound by both %q and %q; only the first would ever run", k, first, b.help)
				continue
			}
			owner[k] = b.help
		}
	}
}

// TestEveryAdvertisedKeyIsBound is the "no advertised but unbound key" rule, in
// both directions.
//
// Forward: every key group the footer or the help overlay prints for a binding
// resolves to keys that binding actually answers. Backward: the keys a binding's
// help text names cover ALL of the keys it answers, so a binding cannot quietly
// grow an alias that nothing documents.
//
// The footer is held to the forward direction only. It has one row and drops
// whole entries under width pressure, so it is allowed to advertise a subset —
// `q quit` for a binding that also answers esc and ctrl+c — and the help overlay
// is where the rest of that subset is named.
func TestEveryAdvertisedKeyIsBound(t *testing.T) {
	for _, b := range gridBindings() {
		bound := map[string]bool{}
		for _, k := range b.keys {
			bound[k] = true
		}

		documented := map[string]bool{}
		for _, display := range strings.Split(b.footerKeysText(), "/") {
			for _, k := range spelled(t, display) {
				if !bound[k] {
					t.Errorf("help prints %q for %q, which the binding does not answer", display, b.help)
				}
				documented[k] = true
			}
		}
		for _, k := range b.keys {
			if !documented[k] {
				t.Errorf("%q answers %q and the help overlay never names it", b.help, k)
			}
		}

		for _, p := range b.parts {
			for _, display := range []string{p.display, p.shortText()} {
				for _, group := range strings.Split(display, "/") {
					for _, k := range spelled(t, group) {
						if !bound[k] {
							t.Errorf("the footer prints %q for %q, which the binding does not answer", group, b.help)
						}
					}
				}
			}
		}
	}
}

// TestOnlyFooterBindingsCarryFooterSpellings keeps the two projections from
// drifting into each other: an entry the footer draws has to have something to
// draw, and an entry it does not draw must not carry a spelling nothing reads.
// The one binding off the row — the undo, whose offer lives on the feedback row
// while it is eligible — is what this exists to keep honest.
func TestOnlyFooterBindingsCarryFooterSpellings(t *testing.T) {
	offRow := 0
	for _, b := range gridBindings() {
		switch {
		case b.onFooter && len(b.parts) == 0:
			t.Errorf("%q is on the footer with no key group to draw", b.help)
		case b.onFooter && b.labelFor == nil:
			t.Errorf("%q is on the footer with no label", b.help)
		case !b.onFooter && len(b.parts) > 0:
			t.Errorf("%q is off the footer but carries footer spellings", b.help)
		}
		if !b.onFooter {
			offRow++
		}
	}
	if offRow != 1 {
		t.Errorf("%d bindings are off the footer, want exactly the undo", offRow)
	}
}

// TestEveryBindingIsHelpedAndSectioned is the help table's admission rule. A
// binding with no sentence would render as a key and an empty line, and one in a
// section the overlay does not order would be generated into nothing at all —
// which is the silent way a key stops being documented.
func TestEveryBindingIsHelpedAndSectioned(t *testing.T) {
	sections := map[string]bool{}
	for _, section := range helpSectionOrder {
		sections[section] = true
	}
	for _, b := range gridBindings() {
		if strings.TrimSpace(b.help) == "" {
			t.Errorf("the binding for %v has no help sentence", b.keys)
		}
		if !sections[b.section] {
			t.Errorf("the binding for %v is in section %q, which helpSectionOrder does not draw", b.keys, b.section)
		}
	}
}
