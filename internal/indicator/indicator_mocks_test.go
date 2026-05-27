package indicator

import (
	"sync"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
)

type fakeIndicatorKittyClient struct {
	windows       []kitty.OSWindow
	listErr       error
	listCalls     int
	screenText    string
	getTextErr    error
	setTitleErr   error
	getTextCalls  []getTextCall
	setTitleCalls []setTitleCall
}

type getTextCall struct {
	match  string
	extent string
}

type setTitleCall struct {
	match string
	title string
}

func (f *fakeIndicatorKittyClient) ListWindows() ([]kitty.OSWindow, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]kitty.OSWindow, len(f.windows))
	copy(out, f.windows)
	return out, nil
}

func (f *fakeIndicatorKittyClient) GetText(matchExpr, extent string) (string, error) {
	f.getTextCalls = append(f.getTextCalls, getTextCall{match: matchExpr, extent: extent})
	if f.getTextErr != nil {
		return "", f.getTextErr
	}
	return f.screenText, nil
}

func (f *fakeIndicatorKittyClient) SetTabTitle(matchExpr, title string) error {
	f.setTitleCalls = append(f.setTitleCalls, setTitleCall{match: matchExpr, title: title})
	return f.setTitleErr
}

type fakeIndicatorDaemonClient struct {
	resp    daemon.Response
	err     error
	reqs    []daemon.Request
	amxBins []string
}

func (f *fakeIndicatorDaemonClient) SendWithAutoStart(req daemon.Request, amxBin string) (daemon.Response, error) {
	f.reqs = append(f.reqs, req)
	f.amxBins = append(f.amxBins, amxBin)
	if f.err != nil {
		return daemon.Response{}, f.err
	}
	return f.resp, nil
}

type handlerBackedIndicatorDaemonClient struct {
	mu   sync.Mutex
	h    *daemon.Handler
	reqs []daemon.Request
}

func (f *handlerBackedIndicatorDaemonClient) SendWithAutoStart(req daemon.Request, amxBin string) (daemon.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	return f.h.Handle(req), nil
}

func (f *handlerBackedIndicatorDaemonClient) snapshot() []daemon.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]daemon.Request, len(f.reqs))
	copy(out, f.reqs)
	return out
}

func fakeIndicatorWindows(windowID int, tabTitle, profile, harnessName string) []kitty.OSWindow {
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
								Profile: profile,
								Harness: harnessName,
							},
						},
					},
				},
			},
		},
	}
}
