//go:build linux

package debug

import (
	"os"

	"golang.org/x/sys/unix"
)

func isTerminalInput(file *os.File) bool {
	if file == nil {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}
