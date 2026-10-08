package debug

import "testing"

func TestSecondsTextDropsZeroUnits(t *testing.T) {
	cases := map[int64]string{
		0:     "0s",
		12:    "12s",
		60:    "1m",
		1802:  "30m2s",
		3600:  "1h",
		3660:  "1h1m",
		3601:  "1h0m1s",
		14400: "4h",
	}
	for seconds, want := range cases {
		if got := secondsText(seconds); got != want {
			t.Errorf("secondsText(%d) = %q, want %q", seconds, got, want)
		}
	}
}
