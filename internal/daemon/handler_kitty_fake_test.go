package daemon

import (
	"strconv"
	"strings"
	"sync"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// fakeKittyClient is the minimum implementation of kittyTitleClient used by
// the stale sweep. It records SetTabTitle invocations and returns canned
// ListWindows data. Thread-safe so tests can assert from the main goroutine
// while the sweep runs (even though the current sweep is synchronous, the
// notifier dispatch under synctest is not).
type fakeKittyClient struct {
	mu               sync.Mutex
	windows          []kitty.OSWindow
	listErr          error
	getText          map[string]string
	getTextByExtent  map[getTextKey]string
	getTextErr       error
	setTitleErr      error
	setUserVarsErr   error
	setTitleCalls    []setTitleCall
	setUserVarsCalls []setUserVarsCall
}

type setTitleCall struct {
	match string
	title string
}

type setUserVarsCall struct {
	match string
	vars  map[string]string
}

type getTextKey struct {
	match  string
	extent string
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

func (f *fakeKittyClient) GetText(matchExpr, extent string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getTextErr != nil {
		return "", f.getTextErr
	}
	if f.getTextByExtent != nil {
		if text, ok := f.getTextByExtent[getTextKey{match: matchExpr, extent: extent}]; ok {
			return text, nil
		}
	}
	return f.getText[matchExpr], nil
}

func (f *fakeKittyClient) SetTabTitle(matchExpr, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setTitleCalls = append(f.setTitleCalls, setTitleCall{match: matchExpr, title: title})
	if f.setTitleErr != nil {
		return f.setTitleErr
	}
	if id, ok := parseFakeWindowIDMatch(matchExpr, "window_id:"); ok {
		for i := range f.windows {
			for j := range f.windows[i].Tabs {
				for k := range f.windows[i].Tabs[j].Windows {
					if f.windows[i].Tabs[j].Windows[k].ID == id {
						f.windows[i].Tabs[j].Title = title
						break
					}
				}
			}
		}
	}
	return nil
}

func (f *fakeKittyClient) SetUserVars(matchExpr string, vars map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(map[string]string, len(vars))
	for k, v := range vars {
		cp[k] = v
	}
	f.setUserVarsCalls = append(f.setUserVarsCalls, setUserVarsCall{match: matchExpr, vars: cp})
	if f.setUserVarsErr != nil {
		return f.setUserVarsErr
	}
	if id, ok := parseFakeWindowIDMatch(matchExpr, "id:"); ok {
		for i := range f.windows {
			for j := range f.windows[i].Tabs {
				for k := range f.windows[i].Tabs[j].Windows {
					win := &f.windows[i].Tabs[j].Windows[k]
					if win.ID != id {
						continue
					}
					if value, ok := vars["amx_state"]; ok {
						win.UserVars.AMXState = value
					}
				}
			}
		}
	}
	return nil
}

// titleCalls returns a copy of recorded SetTabTitle calls.
func (f *fakeKittyClient) titleCalls() []setTitleCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]setTitleCall, len(f.setTitleCalls))
	copy(out, f.setTitleCalls)
	return out
}

func (f *fakeKittyClient) userVarsCalls() []setUserVarsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]setUserVarsCall, len(f.setUserVarsCalls))
	copy(out, f.setUserVarsCalls)
	return out
}

func parseFakeWindowIDMatch(matchExpr, prefix string) (int, bool) {
	if !strings.HasPrefix(matchExpr, prefix) {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimPrefix(matchExpr, prefix))
	return id, err == nil
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
						{
							ID: windowID,
							UserVars: kitty.UserVars{
								Profile: "work",
								Harness: "codex",
							},
						},
					},
				},
			},
		},
	}
}
