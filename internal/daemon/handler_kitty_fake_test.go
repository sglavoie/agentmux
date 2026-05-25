package daemon

import (
	"sync"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// fakeKittyClient is the minimum implementation of kittyTitleClient used by
// the stale sweep. It records SetTabTitle invocations and returns canned
// ListWindows data. Thread-safe so tests can assert from the main goroutine
// while the sweep runs (even though the current sweep is synchronous, the
// notifier dispatch under synctest is not).
type fakeKittyClient struct {
	mu            sync.Mutex
	windows       []kitty.OSWindow
	listErr       error
	getText       map[string]string
	getTextErr    error
	setTitleErr   error
	setTitleCalls []setTitleCall
}

type setTitleCall struct {
	match string
	title string
}

func (f *fakeKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Return a defensive copy so tests cannot accidentally mutate the fake's
	// canned data through the returned slice.
	out := make([]kitty.OSWindow, len(f.windows))
	copy(out, f.windows)
	return out, nil
}

func (f *fakeKittyClient) GetText(matchExpr, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getTextErr != nil {
		return "", f.getTextErr
	}
	return f.getText[matchExpr], nil
}

func (f *fakeKittyClient) SetTabTitle(matchExpr, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setTitleCalls = append(f.setTitleCalls, setTitleCall{match: matchExpr, title: title})
	return f.setTitleErr
}

// titleCalls returns a copy of recorded SetTabTitle calls.
func (f *fakeKittyClient) titleCalls() []setTitleCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]setTitleCall, len(f.setTitleCalls))
	copy(out, f.setTitleCalls)
	return out
}

// fakeOSWindowsWithTab builds an OS-window list with one tab containing one
// window of the given ID and tab title. This is the canonical shape returned
// by `kitten @ ls` that the sweep needs to find via FindTabByWindowID.
func fakeOSWindowsWithTab(windowID int, tabTitle string) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: tabTitle,
					Windows: []kitty.Window{
						{ID: windowID},
					},
				},
			},
		},
	}
}
