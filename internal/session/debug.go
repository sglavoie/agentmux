package session

import (
	"io"
	"log"
	"os"
)

func newFileLogger(path, prefix string) *log.Logger {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return log.New(io.Discard, prefix, 0)
	}
	return log.New(f, prefix, log.LstdFlags)
}
