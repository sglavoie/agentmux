package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type fakeIconPickerClient struct {
	windows    []kitty.OSWindow
	listErr    error
	listCalls  int
	titleCalls []setTabTitleCall
	titleErr   error
	userWrites int
}

type setTabTitleCall struct {
	matchExpr string
	title     string
}

func (f *fakeIconPickerClient) ListWindows() ([]kitty.OSWindow, error) {
	f.listCalls++
	return f.windows, f.listErr
}
func (f *fakeIconPickerClient) LaunchTab(kitty.LaunchOpts) error     { return nil }
func (f *fakeIconPickerClient) LaunchOverlay(kitty.LaunchOpts) error { return nil }
func (f *fakeIconPickerClient) CloseTab(string) error                { return nil }
func (f *fakeIconPickerClient) MoveTab(string, int) error            { return nil }
func (f *fakeIconPickerClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (f *fakeIconPickerClient) SetTabTitle(matchExpr, title string) error {
	f.titleCalls = append(f.titleCalls, setTabTitleCall{matchExpr: matchExpr, title: title})
	return f.titleErr
}
func (f *fakeIconPickerClient) SetUserVars(string, map[string]string) error {
	f.userWrites++
	return nil
}
func (f *fakeIconPickerClient) FocusTab(string) error                  { return nil }
func (f *fakeIconPickerClient) FocusWindow(string) error               { return nil }
func (f *fakeIconPickerClient) GetText(string, string) (string, error) { return "", nil }
func (f *fakeIconPickerClient) LaunchOverlayPager(string) error        { return nil }

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

func mustIconPickerModel(t *testing.T, ctx IconPickerContext) iconPickerModel {
	t.Helper()
	m, err := newIconPickerModel(ctx)
	if err != nil {
		t.Fatalf("newIconPickerModel: %v", err)
	}
	return m
}

func TestIconPickerRowsUseStableHarnessOrderAndOmitMissing(t *testing.T) {
	m := mustIconPickerModel(t, IconPickerContext{HarnessEmojis: map[string]string{
		"codex":   "📟",
		"unknown": "🛸",
		"claude":  "⭕",
		"copilot": "",
	}})

	if got, want := m.items, []iconPickerItem{
		{name: "claude", emoji: "⭕"},
		{name: "codex", emoji: "📟"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %#v, want %#v", got, want)
	}
}

func TestIconPickerInitialCursorUsesExistingBadgeOrFirstRow(t *testing.T) {
	emojis := map[string]string{"claude": "⭕", "copilot": "🚁", "codex": "📟"}
	withBadge := mustIconPickerModel(t, IconPickerContext{
		CurrentTitle:  palette.BuildTitle("🚁project", "bolt", true),
		HarnessEmojis: emojis,
	})
	if withBadge.cursor != 1 {
		t.Fatalf("badge cursor = %d, want 1", withBadge.cursor)
	}

	withoutBadge := mustIconPickerModel(t, IconPickerContext{
		CurrentTitle:  "🛸project",
		HarnessEmojis: emojis,
	})
	if withoutBadge.cursor != 0 {
		t.Fatalf("fallback cursor = %d, want 0", withoutBadge.cursor)
	}
}

func TestIconPickerNavigationWrapsAndCancellationDoesNotWrite(t *testing.T) {
	client := &fakeIconPickerClient{}
	ctx := IconPickerContext{
		Client:        client,
		HarnessEmojis: map[string]string{"claude": "⭕", "copilot": "🚁", "codex": "📟"},
	}
	m := mustIconPickerModel(t, ctx)

	updated, _ := m.Update(iconPickerKey("up"))
	m = updated.(iconPickerModel)
	if m.cursor != 2 {
		t.Fatalf("up from first cursor = %d, want 2", m.cursor)
	}
	updated, _ = m.Update(iconPickerKey("down"))
	m = updated.(iconPickerModel)
	if m.cursor != 0 {
		t.Fatalf("down from last cursor = %d, want 0", m.cursor)
	}
	updated, _ = m.Update(iconPickerKey("k"))
	m = updated.(iconPickerModel)
	if m.cursor != 2 {
		t.Fatalf("k from first cursor = %d, want 2", m.cursor)
	}
	updated, _ = m.Update(iconPickerKey("j"))
	m = updated.(iconPickerModel)
	if m.cursor != 0 {
		t.Fatalf("j from last cursor = %d, want 0", m.cursor)
	}

	for _, key := range []string{"esc", "q"} {
		_, cmd := m.Update(iconPickerKey(key))
		if cmd == nil {
			t.Errorf("%s command is nil, want tea.Quit", key)
		}
	}
	if len(client.titleCalls) != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", len(client.titleCalls))
	}
}

func TestIconPickerAppliesToPlainUnmanagedTitle(t *testing.T) {
	client := &fakeIconPickerClient{}
	m := mustIconPickerModel(t, IconPickerContext{
		Client:        client,
		TabID:         42,
		CurrentTitle:  "shell",
		HarnessEmojis: map[string]string{"codex": "📟"},
	})

	updated, cmd := m.Update(iconPickerKey("enter"))
	m = updated.(iconPickerModel)
	if cmd == nil {
		t.Fatal("enter command is nil, want tea.Quit")
	}
	if !m.applied || m.err != nil {
		t.Fatalf("apply state = applied %v err %v", m.applied, m.err)
	}
	if got, want := client.titleCalls, []setTabTitleCall{{matchExpr: kitty.MatchTabByID(42), title: "📟shell"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SetTabTitle calls = %#v, want %#v", got, want)
	}
	if client.userWrites != 0 {
		t.Fatalf("SetUserVars calls = %d, want 0", client.userWrites)
	}
}

func TestIconPickerSwitchingAndRepeatSelectionDoNotAccumulatePrefixes(t *testing.T) {
	client := &fakeIconPickerClient{}
	emojis := map[string]string{"claude": "⭕", "copilot": "🚁", "codex": "📟"}
	title := "project"

	for _, cursor := range []int{0, 2, 2, 1, 0} {
		m := mustIconPickerModel(t, IconPickerContext{
			Client:        client,
			TabID:         7,
			CurrentTitle:  title,
			HarnessEmojis: emojis,
		})
		m.cursor = cursor
		updated, cmd := m.Update(iconPickerKey("enter"))
		m = updated.(iconPickerModel)
		if cmd == nil || m.err != nil {
			t.Fatalf("cursor %d apply: cmd nil=%v err=%v", cursor, cmd == nil, m.err)
		}
		title = client.titleCalls[len(client.titleCalls)-1].title
	}

	if got, want := title, "⭕project"; got != want {
		t.Fatalf("final title = %q, want %q", got, want)
	}
	for _, call := range client.titleCalls {
		if strings.Count(call.title, "project") != 1 {
			t.Errorf("title accumulated content: %q", call.title)
		}
	}
}

func TestIconPickerPreservesAllPaletteManagedTitleSegments(t *testing.T) {
	client := &fakeIconPickerClient{}
	input := palette.BuildTitleParts(palette.TitleParts{
		Base:    "⭕project / feature",
		State:   "warning",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	m := mustIconPickerModel(t, IconPickerContext{
		Client:        client,
		TabID:         8,
		CurrentTitle:  input,
		HarnessEmojis: map[string]string{"claude": "⭕", "codex": "📟"},
	})
	m.cursor = 1
	m.Update(iconPickerKey("enter"))

	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟project / feature",
		State:   "warning",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	if got := client.titleCalls[0].title; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestIconPickerApplyRefreshesStaleTitleBeforeWriting(t *testing.T) {
	launchTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:  "⭕old-name",
		State: "active",
	})
	liveTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "⭕renamed / live",
		State:   "warning",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	client := &fakeIconPickerClient{windows: []kitty.OSWindow{{
		Tabs: []kitty.Tab{{ID: 8, Title: liveTitle}},
	}}}
	m := mustIconPickerModel(t, IconPickerContext{
		Client:        client,
		TabID:         8,
		CurrentTitle:  launchTitle,
		HarnessEmojis: map[string]string{"claude": "⭕", "codex": "📟"},
	})
	m.cursor = 1
	m.Update(iconPickerKey("enter"))

	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟renamed / live",
		State:   "warning",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	if client.listCalls != 1 {
		t.Fatalf("ListWindows calls = %d, want 1", client.listCalls)
	}
	if got := client.titleCalls[0].title; got != want {
		t.Fatalf("title = %q, want refreshed title %q", got, want)
	}
}

func TestIconPickerApplyRefreshFailureFallsBackToLastKnownTitle(t *testing.T) {
	client := &fakeIconPickerClient{listErr: errors.New("Kitty unavailable")}
	lastKnownTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "⭕project",
		State:   "active",
		Starred: true,
	})
	m := mustIconPickerModel(t, IconPickerContext{
		Client:        client,
		TabID:         8,
		CurrentTitle:  lastKnownTitle,
		HarnessEmojis: map[string]string{"claude": "⭕", "codex": "📟"},
	})
	m.cursor = 1
	m.Update(iconPickerKey("enter"))

	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟project",
		State:   "active",
		Starred: true,
	})
	if got := client.titleCalls[0].title; got != want {
		t.Fatalf("title = %q, want fallback title %q", got, want)
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

func TestIconPickerPreservesUnrecognizedLeadingGlyph(t *testing.T) {
	client := &fakeIconPickerClient{}
	m := mustIconPickerModel(t, IconPickerContext{
		Client:        client,
		CurrentTitle:  "🛸project",
		HarnessEmojis: map[string]string{"codex": "📟"},
	})
	m.Update(iconPickerKey("enter"))

	if got, want := client.titleCalls[0].title, "📟🛸project"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestIconPickerRejectsEmptyConfiguration(t *testing.T) {
	for _, emojis := range []map[string]string{nil, {}, {"claude": "", "unknown": "🛸"}} {
		if _, err := newIconPickerModel(IconPickerContext{HarnessEmojis: emojis}); !errors.Is(err, errNoHarnessIcons) {
			t.Errorf("newIconPickerModel(%#v) error = %v, want %v", emojis, err, errNoHarnessIcons)
		}
	}
}

func TestIconPickerWriteFailureStaysVisibleAndCanRetry(t *testing.T) {
	client := &fakeIconPickerClient{titleErr: errors.New("Kitty unavailable")}
	m := mustIconPickerModel(t, IconPickerContext{
		Client:        client,
		TabID:         9,
		CurrentTitle:  "project",
		HarnessEmojis: map[string]string{"claude": "⭕"},
	})

	updated, cmd := m.Update(iconPickerKey("enter"))
	m = updated.(iconPickerModel)
	if cmd != nil {
		t.Fatal("failed apply returned a command, want overlay to remain open")
	}
	if m.applied || m.err == nil {
		t.Fatalf("failed apply state = applied %v err %v", m.applied, m.err)
	}
	if view := m.View().Content; !strings.Contains(view, "set tab title: Kitty unavailable") {
		t.Fatalf("view does not contain write error: %q", view)
	}

	client.titleErr = nil
	updated, cmd = m.Update(iconPickerKey("enter"))
	m = updated.(iconPickerModel)
	if cmd == nil {
		t.Fatal("successful retry command is nil, want tea.Quit")
	}
	if !m.applied || m.err != nil {
		t.Fatalf("retry state = applied %v err %v", m.applied, m.err)
	}
	if len(client.titleCalls) != 2 {
		t.Fatalf("SetTabTitle calls = %d, want 2", len(client.titleCalls))
	}
}
