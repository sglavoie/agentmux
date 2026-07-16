//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package cmd

import "os"

func isTerminalInput(*os.File) bool { return false }
