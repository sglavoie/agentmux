//go:build !darwin && !linux

package tmux

import "errors"

// processExecutable is unknown on this platform.
func processExecutable(int) (string, error) {
	return "", errors.New("reading another process's executable is unsupported on this platform")
}
