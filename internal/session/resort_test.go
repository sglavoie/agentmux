package session_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// moveCall records one MoveTab invocation against fakeKittyClient.
type moveCall struct {
	Match string
	Delta int
}

// fakeKittyClient is a test double satisfying kitty.KittyClient. It can model
// either a single OS window via tabs or multiple OS windows via windows. It
// mutates the containing window's tab list via MoveTab, simulating Kitty's
// move_tab_forward / move_tab_backward chain of adjacent swaps that preserve
// tab IDs. FocusTab updates the focused-tab marker so focus restoration can be
// asserted.
type fakeKittyClient struct {
	windows    []kitty.OSWindow
	tabs       []kitty.Tab
	focused    int
	moveCalls  []moveCall
	focusCalls []string
}

func (f *fakeKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	windows := f.snapshotWindows()
	focusedWindowID := 0
	if wi, _, ok := f.findTab(f.focused); ok {
		focusedWindowID = windows[wi].ID
	}
	for i := range windows {
		windows[i].IsFocused = windows[i].ID == focusedWindowID
		windows[i].IsActive = windows[i].ID == focusedWindowID
		windows[i].LastFocused = windows[i].ID == focusedWindowID
		for j := range windows[i].Tabs {
			windows[i].Tabs[j].IsActive = windows[i].Tabs[j].ID == f.focused
		}
	}
	return windows, nil
}

func (f *fakeKittyClient) MoveTab(matchExpr string, delta int) error {
	if delta == 0 {
		return nil
	}
	id, err := parseIDMatch(matchExpr)
	if err != nil {
		return err
	}
	wi, idx, ok := f.findTab(id)
	if !ok {
		return fmt.Errorf("fakeKittyClient: tab %d not found", id)
	}
	f.focused = id
	f.moveCalls = append(f.moveCalls, moveCall{Match: matchExpr, Delta: delta})

	step := 1
	if delta < 0 {
		step = -1
		delta = -delta
	}
	tabs := f.tabsForWindow(wi)
	for k := 0; k < delta; k++ {
		next := idx + step
		if next < 0 || next >= len(tabs) {
			return fmt.Errorf("fakeKittyClient: MoveTab swap out of bounds")
		}
		tabs[idx], tabs[next] = tabs[next], tabs[idx]
		idx = next
	}
	return nil
}

func (f *fakeKittyClient) FocusTab(matchExpr string) error {
	id, err := parseIDMatch(matchExpr)
	if err != nil {
		return err
	}
	f.focusCalls = append(f.focusCalls, matchExpr)
	f.focused = id
	return nil
}

func (f *fakeKittyClient) LaunchTab(kitty.LaunchOpts) error                         { return nil }
func (f *fakeKittyClient) CloseTab(string) error                                    { return nil }
func (f *fakeKittyClient) SetTabColor(string, string, string, string, string) error { return nil }
func (f *fakeKittyClient) SetTabTitle(string, string) error                         { return nil }
func (f *fakeKittyClient) FocusWindow(string) error                                 { return nil }
func (f *fakeKittyClient) GetText(string, string) (string, error)                   { return "", nil }
func (f *fakeKittyClient) LaunchOverlayPager(string) error                          { return nil }

func parseIDMatch(matchExpr string) (int, error) {
	rest, ok := strings.CutPrefix(matchExpr, "id:")
	if !ok {
		return 0, fmt.Errorf("fakeKittyClient: expected id: match expression, got %q", matchExpr)
	}
	return strconv.Atoi(rest)
}

func (f *fakeKittyClient) snapshotWindows() []kitty.OSWindow {
	if len(f.windows) == 0 {
		tabsCopy := make([]kitty.Tab, len(f.tabs))
		copy(tabsCopy, f.tabs)
		return []kitty.OSWindow{{ID: 1, Tabs: tabsCopy}}
	}

	windowsCopy := make([]kitty.OSWindow, len(f.windows))
	for i := range f.windows {
		windowsCopy[i] = f.windows[i]
		windowsCopy[i].Tabs = make([]kitty.Tab, len(f.windows[i].Tabs))
		copy(windowsCopy[i].Tabs, f.windows[i].Tabs)
	}
	return windowsCopy
}

func (f *fakeKittyClient) findTab(id int) (int, int, bool) {
	if len(f.windows) == 0 {
		for i, t := range f.tabs {
			if t.ID == id {
				return 0, i, true
			}
		}
		return 0, 0, false
	}

	for wi := range f.windows {
		for ti, t := range f.windows[wi].Tabs {
			if t.ID == id {
				return wi, ti, true
			}
		}
	}
	return 0, 0, false
}

func (f *fakeKittyClient) tabsForWindow(windowIndex int) []kitty.Tab {
	if len(f.windows) == 0 {
		return f.tabs
	}
	return f.windows[windowIndex].Tabs
}

func managedTab(id int, account, title string) kitty.Tab {
	return kitty.Tab{
		ID:    id,
		Title: title,
		Windows: []kitty.Window{{
			ID:       id * 100,
			Title:    title,
			UserVars: kitty.UserVars{Account: account},
		}},
	}
}

func unmanagedTab(id int, title string) kitty.Tab {
	return kitty.Tab{
		ID:      id,
		Title:   title,
		Windows: []kitty.Window{{ID: id * 100, Title: title}},
	}
}

func tabIDs(tabs []kitty.Tab) []int {
	ids := make([]int, len(tabs))
	for i, t := range tabs {
		ids[i] = t.ID
	}
	return ids
}

func TestResort(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		initial   []kitty.Tab
		wantOrder []int
		wantMoves int
	}{
		{
			name: "already sorted alpha does nothing",
			mode: "alpha",
			initial: []kitty.Tab{
				managedTab(1, "personal", "P:alpha"),
				managedTab(2, "personal", "P:beta"),
				managedTab(3, "personal", "P:gamma"),
			},
			wantOrder: []int{1, 2, 3},
			wantMoves: 0,
		},
		{
			name: "already sorted account does nothing",
			mode: "account",
			initial: []kitty.Tab{
				managedTab(1, "personal", "P:amx"),
				managedTab(2, "personal", "P:tt"),
				managedTab(3, "work", "W:api"),
			},
			wantOrder: []int{1, 2, 3},
			wantMoves: 0,
		},
		{
			name: "reversed alpha is repaired",
			mode: "alpha",
			initial: []kitty.Tab{
				managedTab(1, "personal", "P:c"),
				managedTab(2, "personal", "P:b"),
				managedTab(3, "personal", "P:a"),
			},
			wantOrder: []int{3, 2, 1},
			wantMoves: 2,
		},
		{
			name: "screenshot scenario in account mode",
			mode: "account",
			initial: []kitty.Tab{
				managedTab(1, "personal", "P:\U0001F4DDtt"), // 📝tt
				managedTab(2, "tech", "T:Katana"),
				managedTab(3, "vtacyber", "V:sis"),
				managedTab(4, "personal", "P:\U0001F431amx"),  // 🐱amx
				managedTab(5, "vtacyber", "V:\U0001F3B5muse"), // 🎵muse
			},
			wantOrder: []int{4, 1, 2, 5, 3},
			wantMoves: 2,
		},
		{
			name: "unmanaged tab shifts when managed tab slides past",
			mode: "alpha",
			initial: []kitty.Tab{
				managedTab(10, "personal", "P:zeta"),
				unmanagedTab(20, "scratch"),
				managedTab(30, "personal", "P:alpha"),
			},
			// M30 slides from idx 2 to 0 (delta=-2), shifting M10 and U20
			// right by one. M10 then slides from idx 1 to 2 (delta=+1),
			// swapping past U20. Final managed order: [30, 10]; U20 sits
			// between them — the documented trade-off.
			wantOrder: []int{30, 20, 10},
			wantMoves: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tabs := make([]kitty.Tab, len(tc.initial))
			copy(tabs, tc.initial)
			fake := &fakeKittyClient{tabs: tabs}

			if err := session.Resort(fake, tc.mode); err != nil {
				t.Fatalf("Resort: %v", err)
			}

			gotOrder := tabIDs(fake.tabs)
			if !equalInts(gotOrder, tc.wantOrder) {
				t.Errorf("final tab order = %v, want %v", gotOrder, tc.wantOrder)
			}
			if len(fake.moveCalls) != tc.wantMoves {
				t.Errorf("MoveTab call count = %d, want %d (calls: %v)", len(fake.moveCalls), tc.wantMoves, fake.moveCalls)
			}
			for _, c := range fake.moveCalls {
				if !strings.HasPrefix(c.Match, "id:") {
					t.Errorf("expected id: match expression, got %q", c.Match)
				}
			}
		})
	}
}

func TestResort_NoOpWhenSingleTab(t *testing.T) {
	fake := &fakeKittyClient{
		tabs: []kitty.Tab{managedTab(1, "personal", "P:solo")},
	}
	if err := session.Resort(fake, "alpha"); err != nil {
		t.Fatalf("Resort: %v", err)
	}
	if len(fake.moveCalls) != 0 {
		t.Errorf("expected no MoveTab calls for single tab, got %d", len(fake.moveCalls))
	}
}

func TestResort_NoOpWhenNoManagedTabs(t *testing.T) {
	fake := &fakeKittyClient{
		tabs: []kitty.Tab{unmanagedTab(1, "shell"), unmanagedTab(2, "editor")},
	}
	if err := session.Resort(fake, "alpha"); err != nil {
		t.Fatalf("Resort: %v", err)
	}
	if len(fake.moveCalls) != 0 {
		t.Errorf("expected no MoveTab calls when nothing is managed, got %d", len(fake.moveCalls))
	}
}

func TestResort_RestoresFocus(t *testing.T) {
	fake := &fakeKittyClient{
		tabs: []kitty.Tab{
			managedTab(1, "personal", "P:c"),
			managedTab(2, "personal", "P:b"),
			managedTab(3, "personal", "P:a"),
		},
		focused: 2,
	}
	if err := session.Resort(fake, "alpha"); err != nil {
		t.Fatalf("Resort: %v", err)
	}
	if fake.focused != 2 {
		t.Errorf("expected focus restored to tab 2, got %d", fake.focused)
	}
	found := false
	for _, fc := range fake.focusCalls {
		if fc == "id:2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected FocusTab(id:2) to be called for restore, got focusCalls=%v", fake.focusCalls)
	}
}

func TestResort_UnmanagedTabContentsPreserved(t *testing.T) {
	fake := &fakeKittyClient{
		tabs: []kitty.Tab{
			managedTab(10, "personal", "P:zeta"),
			unmanagedTab(20, "scratch"),
			managedTab(30, "personal", "P:alpha"),
		},
	}
	if err := session.Resort(fake, "alpha"); err != nil {
		t.Fatalf("Resort: %v", err)
	}
	var unmanaged *kitty.Tab
	for i := range fake.tabs {
		if fake.tabs[i].ID == 20 {
			unmanaged = &fake.tabs[i]
			break
		}
	}
	if unmanaged == nil {
		t.Fatalf("unmanaged tab id=20 missing from final tab list: %v", tabIDs(fake.tabs))
	}
	if len(unmanaged.Windows) != 1 || unmanaged.Windows[0].ID != 2000 || unmanaged.Title != "scratch" {
		t.Errorf("unmanaged tab contents disturbed: %+v", unmanaged)
	}
}

func TestResort_SortsEachOSWindowIndependently(t *testing.T) {
	fake := &fakeKittyClient{
		windows: []kitty.OSWindow{
			{
				ID: 1,
				Tabs: []kitty.Tab{
					managedTab(10, "personal", "P:b"),
					managedTab(11, "personal", "P:a"),
				},
			},
			{
				ID: 2,
				Tabs: []kitty.Tab{
					unmanagedTab(20, "shell"),
					managedTab(30, "work", "W:z"),
					managedTab(40, "work", "W:a"),
				},
			},
		},
		focused: 30,
	}

	if err := session.Resort(fake, "alpha"); err != nil {
		t.Fatalf("Resort: %v", err)
	}

	if got := tabIDs(fake.windows[0].Tabs); !equalInts(got, []int{11, 10}) {
		t.Errorf("window 1 tab order = %v, want %v", got, []int{11, 10})
	}
	if got := tabIDs(fake.windows[1].Tabs); !equalInts(got, []int{20, 40, 30}) {
		t.Errorf("window 2 tab order = %v, want %v", got, []int{20, 40, 30})
	}
	if fake.focused != 30 {
		t.Errorf("expected focus restored to tab 30, got %d", fake.focused)
	}
	if len(fake.moveCalls) != 2 {
		t.Errorf("MoveTab call count = %d, want 2 (calls: %v)", len(fake.moveCalls), fake.moveCalls)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
