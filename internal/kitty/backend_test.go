package kitty

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/backend/backendtest"
)

func TestBackendContract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) backendtest.Harness {
		return backendtest.Harness{
			Backend:   NewBackend(NewClientWithSocket(newFakeKitty(), "unix:/tmp/amx-contract")),
			Directory: t.TempDir(),
			EchoArgv:  func(marker string) []string { return []string{fakeEchoCommand, marker} },
		}
	})
}

// TestBackendIssuesLegacyArgv pins that the adapter maps each backend
// operation onto exactly the `kitten @` argv the domain code issued before
// the seam existed (see TestRemoteControlBaseline for the client side).
func TestBackendIssuesLegacyArgv(t *testing.T) {
	cases := []struct {
		name string
		call func(*Backend) error
		want []string
	}{
		{"close group", func(b *Backend) error { return b.Close(GroupRef(4)) },
			[]string{"close-tab", "--no-response", "--match=id:4"}},
		{"close pane", func(b *Backend) error { return b.Close(PaneRef(9)) },
			[]string{"close-tab", "--no-response", "--match=window_id:9"}},
		{"focus group", func(b *Backend) error { return b.Focus(GroupRef(4)) },
			[]string{"focus-tab", "--match=id:4"}},
		{"focus pane", func(b *Backend) error { return b.Focus(PaneRef(9)) },
			[]string{"focus-window", "--match=id:9"}},
		{"title via pane", func(b *Backend) error { return b.SetTitle(PaneRef(9), "-x") },
			[]string{"set-tab-title", "--match=window_id:9", "--", "-x"}},
		{"metadata", func(b *Backend) error {
			return b.SetMetadata(PaneRef(9), map[string]string{"amx_locked": "true", "cc_mute": ""})
		}, []string{"set-user-vars", "--match=id:9", "amx_locked=true", "cc_mute="}},
		{"colors", func(b *Backend) error {
			return b.SetColors(GroupRef(4), backend.Colors{ActiveBg: backend.ColorReset, InactiveBg: "#123456", ActiveFg: backend.ColorReset, InactiveFg: "#ffffff"})
		}, []string{"set-tab-color", "--match=id:4", "active_bg=NONE", "inactive_bg=#123456", "active_fg=NONE", "inactive_fg=#ffffff"}},
		{"capture", func(b *Backend) error { _, err := b.Capture(PaneRef(9), backend.ExtentAll); return err },
			[]string{"get-text", "--extent=all", "--match=id:9"}},
		{"launch", func(b *Backend) error {
			return b.Launch(backend.LaunchSpec{Title: "T", Directory: "/d", Placement: backend.PlacementAfter,
				Env: map[string]string{"E": "1"}, Vars: map[string]string{"amx_project": "p"}, Argv: []string{"-sh", "-c"}})
		}, []string{"launch", "--type=tab", "--no-response", "--tab-title=T", "--cwd=/d", "--location=after", "--env=E=1", "--var=amx_project=p", "--", "-sh", "-c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewMockRunner(nil)
			if err := tc.call(NewBackend(NewClientWithSocket(runner, ""))); err != nil {
				t.Fatal(err)
			}
			if len(runner.Calls) != 1 {
				t.Fatalf("calls = %+v, want exactly one", runner.Calls)
			}
			want := append([]string{"@"}, tc.want...)
			if got := runner.Calls[0].Args; !reflect.DeepEqual(got, want) {
				t.Errorf("argv = %q\nwant   %q", got, want)
			}
		})
	}
}

func TestBackendRejectsMatchInjection(t *testing.T) {
	runner := NewMockRunner(nil)
	b := NewBackend(NewClientWithSocket(runner, ""))
	for _, id := range []string{"1 or title:x", "-1", "0", "01", "+1", "1.0", "window_id:1"} {
		for _, ref := range []backend.Ref{backend.GroupID(id), backend.PaneID(id)} {
			if err := b.Close(ref); !errors.Is(err, backend.ErrInvalidRef) {
				t.Errorf("Close(%v) err = %v, want ErrInvalidRef", ref, err)
			}
		}
	}
	if err := b.Launch(backend.LaunchSpec{Placement: "neighbor"}); err == nil {
		t.Error("unknown placement accepted")
	}
	if len(runner.Calls) != 0 {
		t.Errorf("rejected refs reached kitten: %+v", runner.Calls)
	}
}

func TestBackendDiscoverReportsTopology(t *testing.T) {
	runner := NewMockRunner(map[string][]byte{"kitten @ ls": []byte(`[{"id":1,"tabs":[
		{"id":3,"is_active":true,"title":"API","windows":[
			{"id":7,"is_active":true,"title":"claude","cwd":"/p","user_vars":{"amx_profile":"work","amx_locked":"true","cc_mute":""},
			 "foreground_processes":[{"pid":42,"cmdline":["claude"],"cwd":"/p"}]},
			{"id":8,"title":"zsh","cwd":"/q","user_vars":{}}]}]}]`)})
	panes, err := NewBackend(NewClientWithSocket(runner, "")).Discover()
	if err != nil {
		t.Fatal(err)
	}
	want := []backend.Pane{
		{Ref: PaneRef(7), Group: GroupRef(3), GroupTitle: "API", Title: "claude", CWD: "/p",
			Vars:   map[string]string{"amx_profile": "work", "amx_locked": "true"},
			Active: true, GroupActive: true,
			Foreground: []backend.Process{{PID: 42, Argv: []string{"claude"}, CWD: "/p"}}},
		{Ref: PaneRef(8), Group: GroupRef(3), GroupTitle: "API", Title: "zsh", CWD: "/q", GroupActive: true},
	}
	if !reflect.DeepEqual(panes, want) {
		t.Errorf("Discover =\n%+v\nwant\n%+v", panes, want)
	}
}
