//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package debug

import "os"

func isTerminalInput(*os.File) bool { return false }
