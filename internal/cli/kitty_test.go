package cli

import "testing"

// A window ID of 0 means "no window context", which is why every unusable
// environment value collapses to it rather than to an error: commands that can
// work without a window must not be refused because Kitty exported something
// odd.
func TestWindowIDFromEnv(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  int
	}{
		{name: "unset", want: 0},
		{name: "empty", value: "", set: true, want: 0},
		{name: "window", value: "42", set: true, want: 42},
		{name: "not a number", value: "abc", set: true, want: 0},
		{name: "trailing text", value: "42x", set: true, want: 0},
		{name: "negative", value: "-1", set: true, want: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(WindowIDEnv, tc.value)
			} else {
				t.Setenv(WindowIDEnv, "")
			}

			if got := WindowIDFromEnv(); got != tc.want {
				t.Fatalf("WindowIDFromEnv() = %d; want %d", got, tc.want)
			}
		})
	}
}

// An explicit --window-id wins, and it wins even when it disagrees with the
// window the command happens to be running in: that is the whole point of the
// flag on the hook and picker surfaces.
func TestWindowIDOrEnvPrefersTheFlag(t *testing.T) {
	t.Setenv(WindowIDEnv, "7")

	if got := WindowIDOrEnv(42); got != 42 {
		t.Fatalf("WindowIDOrEnv(42) = %d; want the flag value", got)
	}
}

func TestWindowIDOrEnvFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv(WindowIDEnv, "7")

	if got := WindowIDOrEnv(0); got != 7 {
		t.Fatalf("WindowIDOrEnv(0) = %d; want the environment value", got)
	}
}

func TestWindowIDOrEnvReportsNoContext(t *testing.T) {
	t.Setenv(WindowIDEnv, "")

	if got := WindowIDOrEnv(0); got != 0 {
		t.Fatalf("WindowIDOrEnv(0) = %d; want 0 outside Kitty", got)
	}
}

// The real Kitty client is what every command acts through, and it satisfies
// the narrow read-only contract the diagnostic surfaces take.
func TestNewKittyClientSatisfiesTheWindowListerContract(t *testing.T) {
	client := NewKittyClient()
	if client == nil {
		t.Fatal("NewKittyClient returned nil")
	}
	var _ KittyWindowLister = client
}
