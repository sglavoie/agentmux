package transcript

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// JSONL records can contain large tool results. Keep the memory bound above
// the old 16 MiB scanner ceiling, and report a larger record as an error so
// callers never mistake an incomplete transcript for a complete one.
const maxJSONLRecordBytes = 128 << 20

type jsonlScanner struct {
	reader   *bufio.Reader
	maxBytes int
	line     []byte
	err      error
	done     bool
}

func newJSONLScanner(r io.Reader) *jsonlScanner {
	return &jsonlScanner{reader: bufio.NewReaderSize(r, 64<<10), maxBytes: maxJSONLRecordBytes}
}

func (s *jsonlScanner) Scan() bool {
	if s.done {
		return false
	}
	s.line = s.line[:0]
	for {
		chunk, err := s.reader.ReadSlice('\n')
		limit := s.maxBytes
		if err == nil {
			limit++ // The final newline is a delimiter, not record content.
		}
		if len(s.line)+len(chunk) > limit {
			s.err = fmt.Errorf("transcript JSONL record exceeds %d bytes", s.maxBytes)
			s.done = true
			return false
		}
		s.line = append(s.line, chunk...)
		switch {
		case err == nil:
			s.line = s.line[:len(s.line)-1]
			if len(s.line) > 0 && s.line[len(s.line)-1] == '\r' {
				s.line = s.line[:len(s.line)-1]
			}
			return true
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			s.done = true
			return len(s.line) > 0
		default:
			s.err = err
			s.done = true
			return false
		}
	}
}

func (s *jsonlScanner) Bytes() []byte { return s.line }
func (s *jsonlScanner) Err() error    { return s.err }
