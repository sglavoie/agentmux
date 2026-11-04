package customization

import (
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/palette"
)

func iconPickerKey(key string) tea.KeyPressMsg {
	switch key {
	case "j":
		return tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"})
	case "k":
		return tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"})
	case "q":
		return tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"})
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	case "up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp})
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	default:
		panic("unsupported test key: " + key)
	}
}

func mustIconPickerModel(t *testing.T, ctx IconContext) iconPickerModel {
	t.Helper()
	m, err := newIconPickerModel(ctx)
	if err != nil {
		t.Fatalf("newIconPickerModel: %v", err)
	}
	return m
}

func TestIconPickerRowsUseStableHarnessOrderAndOmitMissing(t *testing.T) {
	m := mustIconPickerModel(t, IconContext{HarnessEmojis: map[string]string{
		"codex":   "📟",
		"unknown": "🛸",
		"claude":  "⭕",
		"copilot": "",
	}})

	if got, want := m.items, []iconPickerItem{
		{name: "claude", emoji: "⭕"},
		{name: "codex", emoji: "📟"},
		{name: "None (clear icon)"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %#v, want %#v", got, want)
	}
}

func TestIconPickerInitialCursorUsesExistingBadgeOrFirstRow(t *testing.T) {
	emojis := map[string]string{"claude": "⭕", "copilot": "🚁", "codex": "📟"}
	withBadge := mustIconPickerModel(t, IconContext{
		CurrentTitle:  palette.BuildTitle("🚁project", "bolt", true),
		HarnessEmojis: emojis,
	})
	if withBadge.cursor != 1 {
		t.Fatalf("badge cursor = %d, want 1", withBadge.cursor)
	}

	withoutBadge := mustIconPickerModel(t, IconContext{
		CurrentTitle:  "🛸project",
		HarnessEmojis: emojis,
	})
	if withoutBadge.cursor != 0 {
		t.Fatalf("fallback cursor = %d, want 0", withoutBadge.cursor)
	}
}

func TestIconPickerPrefixOrderingHandlesOverlapsAndDuplicatesDeterministically(t *testing.T) {
	emojis := map[string]string{"claude": "a", "copilot": "ab", "codex": "aa"}
	if got, want := configuredHarnessPrefixes(emojis), []string{"aa", "ab", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prefix order = %#v, want %#v", got, want)
	}
	if got, want := removeConfiguredHarnessPrefix("abproject", emojis), "project"; got != want {
		t.Fatalf("overlapping prefix removal = %q, want %q", got, want)
	}

	duplicates := map[string]string{"claude": "ab", "copilot": "ab", "codex": "z"}
	if got, want := configuredHarnessPrefixes(duplicates), []string{"ab", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deduplicated prefixes = %#v, want %#v", got, want)
	}
	if got, want := removeConfiguredHarnessPrefix("abproject", duplicates), "project"; got != want {
		t.Fatalf("duplicate prefix removal = %q, want %q", got, want)
	}
}

func TestIconPickerRejectsEmptyConfiguration(t *testing.T) {
	for _, emojis := range []map[string]string{nil, {}, {"claude": "", "unknown": "🛸"}} {
		if _, err := newIconPickerModel(IconContext{HarnessEmojis: emojis}); !errors.Is(err, errNoHarnessIcons) {
			t.Errorf("newIconPickerModel(%#v) error = %v, want %v", emojis, err, errNoHarnessIcons)
		}
	}
}

func TestIconPickerSetTitleHookUpdatesSession(t *testing.T) {
	var written []string
	ctx := IconContext{
		CurrentTitle:  "⭕api",
		HarnessEmojis: map[string]string{"claude": "⭕", "codex": "📟"},
		SetTitle: func(title string) error {
			written = append(written, title)
			return nil
		},
	}
	m := mustIconPickerModel(t, ctx)
	updated, _ := m.Update(iconPickerKey("down"))
	updated, cmd := updated.(iconPickerModel).Update(iconPickerKey("enter"))
	m = updated.(iconPickerModel)
	if cmd == nil || !m.applied || m.err != nil {
		t.Fatalf("enter: applied=%v err=%v", m.applied, m.err)
	}
	if len(written) != 1 || written[0] != "📟api" {
		t.Fatalf("titles written = %q; want the icon swapped to 📟api", written)
	}
}
