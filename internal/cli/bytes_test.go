package cli

import "testing"

func TestFormatBytes(t *testing.T) {
	for value, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 25645981: "24.5 MiB", 3 << 30: "3.0 GiB"} {
		if got := FormatBytes(value); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", value, got, want)
		}
	}
}
