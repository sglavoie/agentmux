// Package clipboard exposes a minimal text-copy helper. The current
// implementation is darwin-only and shells out to `pbcopy`.
package clipboard

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrUnsupported is returned by Copy on platforms without a supported backend.
var ErrUnsupported = errors.New("clipboard: unsupported platform")

// Copy writes text to the system clipboard. Returns ErrUnsupported on non-darwin
// platforms or when no backend is available.
func Copy(text string) error {
	return copy(text, runtime.GOOS, exec.LookPath)
}

func copy(text, goos string, lookPath func(string) (string, error)) error {
	if goos != "darwin" {
		return ErrUnsupported
	}
	pbcopy, err := lookPath("pbcopy")
	if err != nil {
		if !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, exec.ErrDot) {
			return fmt.Errorf("clipboard: find pbcopy: %w", err)
		}
		return ErrUnsupported
	}
	cmd := exec.Command(pbcopy)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard: pbcopy: %w", err)
	}
	return nil
}
