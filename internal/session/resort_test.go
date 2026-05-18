package session_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// detachCall records one DetachTab invocation against fakeKittyClient.
type detachCall struct {
	Source string
	Target string
}

// fakeKittyClient is a test double satisfying kitty.KittyClient. It models a
// single OS window with an ordered tab list and mutates that list in response
// to DetachTab calls (the only mutator Resort needs). All other interface
// methods are stubbed.
type fakeKittyClient struct {
	tabs  []kitty.Tab
	calls []detachCall
}

func (f *fakeKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	tabsCopy := make([]kitty.Tab, len(f.tabs))
	copy(tabsCopy, f.tabs)
	return []kitty.OSWindow{{ID: 1, Tabs: tabsCopy}}, nil
}

func (f *fakeKittyClient) DetachTab(matchExpr, targetMatchExpr string) error {
	src, err := parseIDMatch(matchExpr)
	if err != nil {
		return err
	}
	dst, err := parseIDMatch(targetMatchExpr)
	if err != nil {
		return err
	}
	f.calls = append(f.calls, detachCall{Source: matchExpr, Target: targetMatchExpr})

	srcIdx := -1
	for i, t := range f.tabs {
		if t.ID == src {
			srcIdx = i
			break
		}
	}
	if srcIdx < 0 {
		return fmt.Errorf("fakeKittyClient: source tab %d not found", src)
	}
	srcTab := f.tabs[srcIdx]
	f.tabs = append(f.tabs[:srcIdx], f.tabs[srcIdx+1:]...)

	dstIdx := -1
	for i, t := range f.tabs {
		if t.ID == dst {
			dstIdx = i
			break
		}
	}
	if dstIdx < 0 {
		return fmt.Errorf("fakeKittyClient: target tab %d not found", dst)
	}
	newTabs := make([]kitty.Tab, 0, len(f.tabs)+1)
	newTabs = append(newTabs, f.tabs[:dstIdx+1]...)
	newTabs = append(newTabs, srcTab)
	newTabs = append(newTabs, f.tabs[dstIdx+1:]...)
	f.tabs = newTabs
	return nil
}

func (f *fakeKittyClient) LaunchTab(kitty.LaunchOpts) error               { return nil }
func (f *fakeKittyClient) CloseTab(string) error                          { return nil }
func (f *fakeKittyClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (f *fakeKittyClient) SetTabTitle(string, string) error      { return nil }
func (f *fakeKittyClient) FocusTab(string) error                 { return nil }
func (f *fakeKittyClient) FocusWindow(string) error              { return nil }
func (f *fakeKittyClient) GetText(string, string) (string, error) { return "", nil }
func (f *fakeKittyClient) LaunchOverlayPager(string) error       { return nil }

func parseIDMatch(matchExpr string) (int, error) {
	rest, ok := strings.CutPrefix(matchExpr, "id:")
	if !ok {
		return 0, fmt.Errorf("fakeKittyClient: expected id: match expression, got %q", matchExpr)
	}
	return strconv.Atoi(rest)
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
		ID:    id,
		Title: title,
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
				managedTab(1, "personal", "P:\U0001F4DDtt"),     // 📝tt
				managedTab(2, "tech", "T:Katana"),
				managedTab(3, "vtacyber", "V:sis"),
				managedTab(4, "personal", "P:\U0001F431amx"),    // 🐱amx
				managedTab(5, "vtacyber", "V:\U0001F3B5muse"),   // 🎵muse
			},
			wantOrder: []int{4, 1, 2, 5, 3},
			wantMoves: 6,
		},
		{
			name: "unmanaged tab interleaved stays in the list",
			mode: "alpha",
			initial: []kitty.Tab{
				managedTab(10, "personal", "P:zeta"),
				unmanagedTab(20, "scratch"),
				managedTab(30, "personal", "P:alpha"),
			},
			// Order of managed tabs after resort: 30, 10. Unmanaged tab is
			// never moved directly but shifts relative position as a side
			// effect of moving 10 to after 30.
			wantOrder: []int{20, 30, 10},
			wantMoves: 1,
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
			if len(fake.calls) != tc.wantMoves {
				t.Errorf("DetachTab call count = %d, want %d (calls: %v)", len(fake.calls), tc.wantMoves, fake.calls)
			}
			// Sanity: every detach call must use id: match expressions.
			for _, c := range fake.calls {
				if !strings.HasPrefix(c.Source, "id:") || !strings.HasPrefix(c.Target, "id:") {
					t.Errorf("expected id: match expressions, got source=%q target=%q", c.Source, c.Target)
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
	if len(fake.calls) != 0 {
		t.Errorf("expected no DetachTab calls for single tab, got %d", len(fake.calls))
	}
}

func TestResort_NoOpWhenNoManagedTabs(t *testing.T) {
	fake := &fakeKittyClient{
		tabs: []kitty.Tab{unmanagedTab(1, "shell"), unmanagedTab(2, "editor")},
	}
	if err := session.Resort(fake, "alpha"); err != nil {
		t.Fatalf("Resort: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("expected no DetachTab calls when nothing is managed, got %d", len(fake.calls))
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
