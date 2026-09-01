// Digests and output parsing: the deterministic reductions the evidence record
// is built from.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// parseHarnessCompat reduces `asl harness-compat --json` to the rows the record
// keeps. The payload is trusted for shape but not for content: a harness the
// policy classifies as anything other than supported is reported as-is rather
// than rejected here, because the strict/non-strict decision belongs to the
// step's own exit status.
func parseHarnessCompat(output string) ([]HarnessVersion, error) {
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Harnesses     []struct {
			Harness string `json:"harness"`
			Version string `json:"version"`
			Status  string `json:"status"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil, fmt.Errorf("parsing harness compatibility report: %w", err)
	}
	if len(payload.Harnesses) == 0 {
		return nil, errors.New("harness compatibility report named no harnesses")
	}
	versions := make([]HarnessVersion, 0, len(payload.Harnesses))
	for _, row := range payload.Harnesses {
		versions = append(versions, HarnessVersion{
			Harness: row.Harness,
			Version: row.Version,
			Status:  row.Status,
		})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Harness < versions[j].Harness })
	return versions, nil
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// sha256Tree digests every file under root whose name ends in suffix, in
// ascending path order, over the pre-image "<relative path>\n<file digest>\n".
//
// This is the same construction aslcontract uses for the corpus digest, and it
// is deliberate: paths participate, so a renamed file changes the digest, and
// the ordering is fixed, so two machines agree.
func sha256Tree(root, suffix string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no %s files under %s", suffix, root)
	}
	sort.Strings(paths)

	digest := sha256.New()
	for _, rel := range paths {
		fileSum, err := sha256File(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(digest, "%s\n%s\n", filepath.ToSlash(rel), fileSum)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func firstLine(s string) string {
	trimmed := strings.TrimSpace(s)
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// tailBytes bounds a recorded failure tail. Large enough for a Go test failure
// with its stack, small enough that a record stays readable.
const tailBytes = 4000

func tail(output string) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) <= tailBytes {
		return trimmed
	}
	return "...(truncated)...\n" + trimmed[len(trimmed)-tailBytes:]
}
