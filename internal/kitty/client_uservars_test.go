package kitty_test

import (
	"encoding/json"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestSetUserVars_Argv(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	err := client.SetUserVars("id:7", map[string]string{
		"cc_account":  "work",
		"amx_profile": "work",
	})
	if err != nil {
		t.Fatalf("SetUserVars: %v", err)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	got := runner.Calls[0].Args
	want := []string{"@", "set-user-vars", "--match=id:7", "amx_profile=work", "cc_account=work"}
	if len(got) != len(want) {
		t.Fatalf("argv length: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d]: got %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSetUserVars_EmptyIsNoOp(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")

	if err := client.SetUserVars("id:7", nil); err != nil {
		t.Fatalf("SetUserVars(nil): %v", err)
	}
	if err := client.SetUserVars("id:7", map[string]string{}); err != nil {
		t.Fatalf("SetUserVars(empty): %v", err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("expected 0 kitten calls for empty vars, got %d: %v", len(runner.Calls), runner.Calls)
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

func TestGetText_EmptyExtentUsesKittyDefault(t *testing.T) {
	want := "default screen text\n"
	runner := kitty.NewMockRunner(map[string][]byte{
		"kitten @ get-text --match=id:7": []byte(want),
	})
	client := kitty.NewClientWithSocket(runner, "")

	got, err := client.GetText("id:7", "")
	if err != nil {
		t.Fatalf("GetText: %v", err)
	}
	if got != want {
		t.Errorf("GetText: got %q, want %q", got, want)
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("expected 1 kitten call, got %d", len(runner.Calls))
	}
	for _, arg := range runner.Calls[0].Args {
		if arg == "--extent=" {
			t.Fatalf("empty extent should not be passed to kitten, args=%v", runner.Calls[0].Args)
		}
	}
}

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

func TestUserVarsUnmarshal_ExplicitFlagsPresence(t *testing.T) {
	raw := `{"cc_account":"work","cc_flags":"--account-flag","cc_explicit_flags":"","cc_cwd":"/home/user/api"}`
	var uv kitty.UserVars
	if err := json.Unmarshal([]byte(raw), &uv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if uv.ExplicitFlags != "" {
		t.Errorf("ExplicitFlags: got %q, want empty", uv.ExplicitFlags)
	}
	if !uv.ExplicitFlagsSet {
		t.Error("expected ExplicitFlagsSet to be true when cc_explicit_flags is present")
	}
}

func TestUserVarsUnmarshal_ExplicitFlagsAbsent(t *testing.T) {
	raw := `{"cc_account":"work","cc_flags":"--legacy-flag","cc_cwd":"/home/user/api"}`
	var uv kitty.UserVars
	if err := json.Unmarshal([]byte(raw), &uv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if uv.ExplicitFlagsSet {
		t.Error("expected ExplicitFlagsSet to be false when cc_explicit_flags is absent")
	}
}
