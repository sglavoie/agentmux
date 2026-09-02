package cmd

import "os"

// failingWriter is the shared "the command's output stream refuses writes"
// stub. It was defined beside the config tests until session 22 moved that
// family out; the copy here serves the leaves that stayed behind.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrPermission
}
