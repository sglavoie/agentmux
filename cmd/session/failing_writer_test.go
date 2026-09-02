package session

import "os"

// failingWriter is the shared "the command's output stream refuses writes"
// stub. Root cmd keeps its own copy for the leaves that stayed behind; this one
// serves the resort command's write-error path.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrPermission
}
