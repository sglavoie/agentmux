package kitty_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("loading fixture %s: %v", name, err)
	}
	return data
}

func TestListWindows_ParsesJSON(t *testing.T) {
	fixture := loadFixture(t, "kitty_ls.json")
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")

	windows, err := client.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("expected 1 OS window, got %d", len(windows))
	}
	if len(windows[0].Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(windows[0].Tabs))
	}

	tab1 := windows[0].Tabs[0]
	if tab1.Title != "P:dotfiles" {
		t.Errorf("tab 1 title: got %q, want %q", tab1.Title, "P:dotfiles")
	}
	win := tab1.Windows[0]
	if win.UserVars.Account != "personal" {
		t.Errorf("user var cc_account: got %q, want %q", win.UserVars.Account, "personal")
	}
}

func TestListWindows_Empty(t *testing.T) {
	fixture := loadFixture(t, "kitty_ls_empty.json")
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": fixture,
	})
	client := kitty.NewClientWithSocket(runner, "")

	windows, err := client.ListWindows()
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(windows[0].Tabs) != 0 {
		t.Errorf("expected 0 tabs, got %d", len(windows[0].Tabs))
	}
}

func TestListWindows_InvalidJSON(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ ls": []byte("not-json"),
	})
	client := kitty.NewClientWithSocket(runner, "")

	_, err := client.ListWindows()
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLaunchTab_CallsKitten(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	err := client.LaunchTab(kitty.LaunchOpts{
		Title:     "W:api",
		Directory: "~/projects/api",
		Env:       map[string]string{"DEBUG": "1"},
		UserVars:  map[string]string{"cc_account": "work"},
		Cmdline:   []string{"claude"},
	})
	if err != nil {
		t.Fatalf("LaunchTab: %v", err)
	}
	if len(runner.Calls) == 0 {
		t.Fatal("expected kitten to be called")
	}
	if runner.Calls[0].Name != "kitten" {
		t.Errorf("expected 'kitten', got %q", runner.Calls[0].Name)
	}
}

func TestCloseTab_MatchExpr(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	_ = client.CloseTab("id:42")

	found := false
	for _, c := range runner.Calls {
		for _, a := range c.Args {
			if a == "--match=id:42" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected --match=id:42 in kitten @ close-tab args")
	}
}

func TestLaunchOverlayPager_Args(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	_ = client.LaunchOverlayPager("id:7")

	if len(runner.Calls) == 0 {
		t.Fatal("expected kitten to be called")
	}
	args := runner.Calls[0].Args
	wantArgs := map[string]bool{
		"--type=overlay":                  false,
		"--source-window=id:7":            false,
		"--stdin-source=@screen_scrollback": false,
	}
	for _, a := range args {
		if _, ok := wantArgs[a]; ok {
			wantArgs[a] = true
		}
	}
	for arg, found := range wantArgs {
		if !found {
			t.Errorf("expected %q in kitten launch args", arg)
		}
	}
}

func TestGetText_ReturnsText(t *testing.T) {
	want := "Hello from terminal\nLine 2\n"
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ get-text --extent=screen --match=id:7": []byte(want),
	})
	client := kitty.NewClientWithSocket(runner, "")

	got, err := client.GetText("id:7", "screen")
	if err != nil {
		t.Fatalf("GetText: %v", err)
	}
	if got != want {
		t.Errorf("GetText: got %q, want %q", got, want)
	}
}

// Verify UserVars JSON unmarshalling works correctly.
func TestUserVarsUnmarshal(t *testing.T) {
	raw := `{"cc_account":"work","cc_config_dir":"~/.config/claude-work","cc_flags":"--dangerously-skip-permissions","cc_pw":"","cc_env":"","cc_groups":"backend","cc_mute":"","cc_cwd":"/home/user/api"}`
	var uv kitty.UserVars
	if err := json.Unmarshal([]byte(raw), &uv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if uv.Account != "work" {
		t.Errorf("Account: got %q, want %q", uv.Account, "work")
	}
	if uv.Groups != "backend" {
		t.Errorf("Groups: got %q, want %q", uv.Groups, "backend")
	}
}
