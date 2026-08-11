package diagnostics

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func readJSONL(path string, visit func(TraceEnvelope)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), int(MaxSegmentBytes)+1)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var envelope TraceEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return fmt.Errorf("diagnostics: decoding %s line %d: %w", path, line, err)
		}
		visit(envelope)
	}
	return scanner.Err()
}

func readJSONLTolerant(path string, visit func(TraceEnvelope)) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), int(MaxSegmentBytes)+1)
	corrupt := 0
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var envelope TraceEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			corrupt++
			continue
		}
		visit(envelope)
	}
	return corrupt, scanner.Err()
}

// tolerantJSONLParts separates complete valid records from corrupt or partial
// lines while preserving valid records that follow a damaged one.
func tolerantJSONLParts(data []byte) (valid [][]byte, invalid []byte, corrupt int) {
	for offset := 0; offset < len(data); {
		relativeEnd := bytes.IndexByte(data[offset:], '\n')
		if relativeEnd < 0 {
			line := bytes.TrimSpace(data[offset:])
			if len(line) > 0 {
				invalid = append(invalid, data[offset:]...)
				corrupt++
			}
			break
		}
		end := offset + relativeEnd
		line := bytes.TrimSpace(data[offset:end])
		if len(line) > 0 {
			if json.Valid(line) {
				valid = append(valid, append([]byte(nil), line...))
			} else {
				invalid = append(invalid, data[offset:end+1]...)
				corrupt++
			}
		}
		offset = end + 1
	}
	return valid, invalid, corrupt
}

func repairJSONLTail(path string, now time.Time) (string, error) {
	if err := validateRegularTarget(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	valid, invalid := validJSONLPrefix(data)
	if len(invalid) == 0 {
		return "", nil
	}
	quarantine := path + ".partial-" + timestamp(now)
	if err := atomicWritePrivate(quarantine, invalid); err != nil {
		return "", err
	}
	if err := atomicWritePrivate(path, valid); err != nil {
		return "", err
	}
	return quarantine, nil
}

func validJSONLPrefix(data []byte) (valid, invalid []byte) {
	offset := 0
	for offset < len(data) {
		relativeEnd := bytes.IndexByte(data[offset:], '\n')
		if relativeEnd < 0 {
			return data[:offset], data[offset:]
		}
		end := offset + relativeEnd
		line := bytes.TrimSpace(data[offset:end])
		if len(line) > 0 && !json.Valid(line) {
			return data[:offset], data[offset:]
		}
		offset = end + 1
	}
	return data, nil
}
