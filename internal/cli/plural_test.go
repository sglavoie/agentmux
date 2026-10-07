package cli

import "testing"

func TestCountOfAgreesWithTheCount(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "0 plans"}, {1, "1 plan"}, {2, "2 plans"}} {
		if got := CountOf(tc.n, "plan", "plans"); got != tc.want {
			t.Errorf("CountOf(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
	if got := ByCount(1, "has", "have") + " " + ByCount(3, "has", "have"); got != "has have" {
		t.Errorf("ByCount = %q", got)
	}
}
