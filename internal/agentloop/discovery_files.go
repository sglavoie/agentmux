package agentloop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func hasRunFinishedAt(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return strings.TrimSpace(value) != ""
}

type lockInfo struct {
	pid       int
	planDir   string
	createdAt string
}

func readLock(path string) (lockInfo, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockInfo{}, false
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return lockInfo{}, false
	}

	pid, ok := coercePositiveInt(raw["pid"])
	if !ok {
		return lockInfo{}, false
	}
	planDir, ok := raw["plan_dir"].(string)
	if !ok || planDir == "" {
		return lockInfo{}, false
	}

	createdAt, _ := raw["created_at"].(string)
	return lockInfo{pid: pid, planDir: planDir, createdAt: createdAt}, true
}

func readState(path string) (stateFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return stateFile{}, false
	}

	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return stateFile{}, false
	}
	return state, true
}

func coercePositiveInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		if v <= 0 || v > float64(maxIntValue) || v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 0)
		if err != nil || n <= 0 || n > int64(maxIntValue) {
			return 0, false
		}
		return int(n), true
	case int:
		return v, v > 0
	case int64:
		if v <= 0 || v > int64(maxIntValue) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

func parseTimestamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func expandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func pathSHA256(path string) string {
	canonical, err := CanonicalizePath(path)
	if err != nil {
		canonical = filepath.Clean(path)
	}
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func isHexSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
