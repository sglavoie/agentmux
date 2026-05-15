// Package clipboard exposes a minimal text-copy helper. The current
// implementation is darwin-only and shells out to `pbcopy`.
package clipboard

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// ErrUnsupported is returned by Copy on platforms without a supported backend.
var ErrUnsupported = errors.New("clipboard: unsupported platform")

// Copy writes text to the system clipboard. Returns ErrUnsupported on non-darwin
// platforms or when no backend is available.
func Copy(text string) error {
	if runtime.GOOS != "darwin" {
		return ErrUnsupported
	}
	cmd := exec.Command("pbcopy")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("clipboard: stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("clipboard: start pbcopy: %w", err)
	}
	if _, err := stdin.Write([]byte(text)); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("clipboard: write: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("clipboard: close stdin: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("clipboard: pbcopy: %w", err)
	}
	return nil
}
