package kitty

import (
	"os"
	"strconv"
)

// WindowIDEnv is the Kitty-provided environment variable naming the window a
// process was launched inside. Kitty sets it for every window it spawns, so it
// is how a process with no window argument learns its own context.
const WindowIDEnv = "KITTY_WINDOW_ID"

// WindowIDFromEnv reports the Kitty window this process is running in, or 0
// when it is not running inside a Kitty window or the value is unusable.
//
// A zero result is not an error: it is "no window context", which every caller
// is expected to have an answer for. A negative value collapses to zero too,
// which is where this differs from internal/cli.WindowIDFromEnv: that one is
// the CLI's flag-and-environment resolver and carries a negative through so an
// operator sees the value they supplied, while this one is read by code that
// only ever compares the result against a real window ID.
func WindowIDFromEnv() int {
	id, err := strconv.Atoi(os.Getenv(WindowIDEnv))
	if err != nil || id <= 0 {
		return 0
	}
	return id
}
