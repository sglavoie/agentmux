package cli

import (
	"os"
	"strconv"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// WindowIDEnv is the Kitty-provided environment variable that tells an amx
// invocation which window it was launched from. Kitty sets it for every window
// it spawns, so it is how a command with no --window-id flag learns its own
// context.
const WindowIDEnv = "KITTY_WINDOW_ID"

// NewKittyClient assembles the real Kitty client every command acts through.
// It is the one place the runner is chosen, so a command package never has to
// name kitty.NewRealRunner itself.
func NewKittyClient() kitty.KittyClient {
	return kitty.NewClient(kitty.NewRealRunner())
}

// KittyWindowLister is the read-only slice of the Kitty client that the
// diagnostic and export surfaces need: they only ever enumerate the window
// tree, and taking the narrow contract is what lets their tests supply a
// fixture instead of a live Kitty.
type KittyWindowLister interface {
	ListWindows() ([]kitty.OSWindow, error)
}

// WindowIDFromEnv reports the Kitty window the CLI was invoked from, or 0 when
// the process is not running inside a Kitty window (or the value is unusable).
// A zero result is not an error: commands that can work without a window
// context treat it as "no context".
func WindowIDFromEnv() int {
	s := os.Getenv(WindowIDEnv)
	if s == "" {
		return 0
	}
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return id
}

// WindowIDOrEnv resolves the window a command should act on: an explicit
// --window-id flag wins, and the environment answers when the flag is unset.
func WindowIDOrEnv(flag int) int {
	if flag != 0 {
		return flag
	}
	return WindowIDFromEnv()
}
