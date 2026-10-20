package session

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseSinceWindow reads `--since`: any Go duration ("12h", "90m") or a whole
// number of days ("7d") or weeks ("2w"), which Go's own parser has no unit for
// and which are the windows people actually think in for past sessions.
func parseSinceWindow(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	for _, unit := range []struct {
		suffix, name string
		length       time.Duration
	}{{"d", "days like 7d", 24 * time.Hour}, {"w", "weeks like 2w", 7 * 24 * time.Hour}} {
		count, ok := strings.CutSuffix(value, unit.suffix)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(count)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("--since %q: want a positive whole number of %s", value, unit.name)
		}
		return time.Duration(n) * unit.length, nil
	}
	window, err := time.ParseDuration(value)
	if err != nil || window <= 0 {
		return 0, fmt.Errorf("--since %q: want a positive duration like 12h, 7d, or 2w", value)
	}
	return window, nil
}
