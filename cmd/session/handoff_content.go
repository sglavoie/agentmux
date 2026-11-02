package session

import (
	"errors"
	"github.com/sglavoie/agentmux/internal/handoff"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Limit the read before allocating and refuse special files that can block.
func readReviewedHandoff(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("reviewed context must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("reviewed context must be a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, handoff.MaxReviewedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > handoff.MaxReviewedBytes || !utf8.Valid(content) || strings.TrimSpace(string(content)) == "" {
		return nil, errors.New("reviewed context must be non-empty UTF-8 and at most 1 MiB")
	}
	return content, nil
}
