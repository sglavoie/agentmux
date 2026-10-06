package debug

// The bridge's own last words, read back from the daemon's private log.
//
// The published capability block says only "bridge exited", deliberately: it
// reaches every subscriber, and a child's stderr is ASL-authored text. But the
// operator running doctor owns the daemon log the full error was written to,
// and the line in it — an interpreter's SyntaxError, a missing module — is
// usually the whole diagnosis. Without it, "bridge exited" fifty times in a row
// reads as a resolution problem and sends the operator to a fix that changes
// nothing.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// aslBridgeExitedMessage is the daemon log record the bridge manager writes
// each time a child exits and is about to be restarted.
const aslBridgeExitedMessage = "daemon: asl bridge exited; restarting"

// aslBridgeLogTailBytes bounds how much of the daemon log doctor reads. A
// crash loop writes one record per backoff interval, so the most recent one is
// always near the end.
const aslBridgeLogTailBytes = 256 << 10

// aslBridgeFailureMaxChars bounds the rendered error the way the published
// reason is bounded, so one pathological stderr line cannot flood the report.
const aslBridgeFailureMaxChars = 300

// doctorLastASLBridgeFailure is a seam so tests do not read the real log.
var doctorLastASLBridgeFailure = func() string {
	paths, err := doctorDiagnosticPaths()
	if err != nil || paths.DaemonLogFile == "" {
		return ""
	}
	return lastASLBridgeFailure(paths.DaemonLogFile)
}

// lastASLBridgeFailure returns the error of the newest bridge-exit record in
// the daemon log at path, or "" when there is none or the log cannot be read.
// It is advisory: any read problem degrades to saying nothing.
func lastASLBridgeFailure(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - aslBridgeLogTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	marker := []byte(aslBridgeExitedMessage)
	last := ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, marker) {
			continue
		}
		var record struct {
			Msg   string `json:"msg"`
			Error string `json:"error"`
		}
		// The first line after the seek is usually partial; it fails to decode
		// and is skipped like any other unreadable line.
		if json.Unmarshal(line, &record) != nil || record.Msg != aslBridgeExitedMessage {
			continue
		}
		if msg := flattenBridgeFailure(record.Error); msg != "" {
			last = msg
		}
	}
	return last
}

func flattenBridgeFailure(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if runes := []rune(msg); len(runes) > aslBridgeFailureMaxChars {
		msg = string(runes[:aslBridgeFailureMaxChars-1]) + "…"
	}
	return msg
}
