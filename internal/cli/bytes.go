package cli

import "fmt"

// FormatBytes is a byte count for a person: "512 B", "24.5 MiB". Scripts get
// raw counts from --json or piped output instead, so this is never parsed.
func FormatBytes(v int64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	value, suffix := float64(v)/unit, "KiB"
	for _, next := range []string{"MiB", "GiB", "TiB"} {
		if value < unit {
			break
		}
		value, suffix = value/unit, next
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}
