package overseer

// The corpus half of the exclusion invariant.
//
// The other tests in this package assert what an excluded project's answer says.
// This one asserts what its files look like afterwards: a project that has
// declined stays byte-for-byte and mtime-for-mtime as it was, however it is
// asked about, and nothing from inside it appears in any answer. Identity
// discovery is permitted — the command has to read the header line naming a
// conversation's working directory to know whose conversation it is — so the
// invariant is not "never opened" but "never written and never disclosed".

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// excludedSecret appears only inside the excluded project's files. It is
// distinct from historySecret, which the permitted fixtures carry and which an
// authorized answer is supposed to contain.
const excludedSecret = "SECRET-EXCLUDED-PROJECT"

// fileStamp is everything about a file that a read must not change.
type fileStamp struct {
	size    int64
	mode    fs.FileMode
	modTime int64
	digest  string
}

func (s fileStamp) String() string {
	return fmt.Sprintf("size=%d mode=%s mtime=%d sha256=%s", s.size, s.mode, s.modTime, s.digest)
}

// snapshotTree stamps every file under root, so a later snapshot of the same
// tree says not only whether anything changed but which file and how.
func snapshotTree(t *testing.T, root string) map[string]fileStamp {
	t.Helper()
	stamps := map[string]fileStamp{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		stamps[relative] = fileStamp{
			size:    info.Size(),
			mode:    info.Mode(),
			modTime: info.ModTime().UnixNano(),
			digest:  hex.EncodeToString(sum[:]),
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	if len(stamps) == 0 {
		t.Fatalf("snapshot %s found no files; the fixture is not staged", root)
	}
	return stamps
}

// assertTreeUnchanged reports every difference rather than the first, so a
// failure says how far the boundary was crossed.
func assertTreeUnchanged(t *testing.T, label string, before, after map[string]fileStamp) {
	t.Helper()
	names := map[string]struct{}{}
	for name := range before {
		names[name] = struct{}{}
	}
	for name := range after {
		names[name] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	for _, name := range ordered {
		was, existed := before[name]
		is, exists := after[name]
		switch {
		case existed && !exists:
			t.Errorf("%s: %s was removed", label, name)
		case !existed && exists:
			t.Errorf("%s: %s was created (%s)", label, name, is)
		case was != is:
			t.Errorf("%s: %s changed\n before: %s\n  after: %s", label, name, was, is)
		}
	}
}

// writeExcludedTranscript stages a transcript whose content is the excluded
// secret. Its header still names the working directory, because that is the
// only way the command can attribute it and refuse it.
func writeExcludedTranscript(t *testing.T, home, encodedDir, sessionID, cwd string) string {
	t.Helper()
	path := filepath.Join(home, "projects", encodedDir, sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2026-08-01T10:00:00.000Z","sessionId":"` + sessionID + `","cwd":"` + cwd +
			`","message":{"role":"user","content":[{"type":"text","text":"` + excludedSecret + ` from ` + sessionID + `"}]}}`,
		`{"type":"assistant","timestamp":"2026-08-01T10:00:05.000Z","sessionId":"` + sessionID + `","cwd":"` + cwd +
			`","message":{"role":"assistant","content":[{"type":"text","text":"` + excludedSecret + ` reply in ` + sessionID + `"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func TestExcludedProjectIsNeitherDisclosedNorTouched(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	home := filepath.Join(root, "claude-home")
	app := filepath.Join(root, "app")
	excluded := filepath.Join(root, "vendor")
	for _, dir := range []string{app, excluded} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	credentials := filepath.Join(excluded, "credentials.txt")
	if err := os.WriteFile(credentials, []byte(excludedSecret+"\n"), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	writeClaudeTranscript(t, home, "-app", "app-session", app)
	excludedTranscript := writeExcludedTranscript(t, home, "-vendor", "vendor-session", excluded)

	writeConfigFile(t, configPath(t), renderHistoryConfig(home,
		projectFixture{title: "App", directory: app, optIn: "true"},
		projectFixture{title: "Vendor", directory: excluded, optIn: "false"},
	))

	repoBefore := snapshotTree(t, excluded)
	conversationsBefore := snapshotTree(t, filepath.Dir(excludedTranscript))

	// Asked about by name: refused, and refused by declaration rather than by
	// anything read out of the project.
	refusal, _, err := executePolicy(t, "--json", "--access", "mutation", "--plan", "plan-1", "--repository", "vendor="+excluded)
	requireQuietExit(t, err)
	decision := decodePolicy(t, refusal).Data
	if decision.Allowed {
		t.Fatalf("the excluded project was permitted: %s", refusal)
	}
	if len(decision.Repositories) != 1 || decision.Repositories[0].Reason != "declined" {
		t.Fatalf("repository outcome = %+v, want one repository declined", decision.Repositories)
	}

	// Asked about beside a permitted project: discovered by identity, withheld
	// by name only, and its content never carried.
	stdout, _, err := executeHistory(t, "--json", "--plan", "plan-1", "--repository", "app="+app)
	if err != nil {
		t.Fatalf("history returned %v\n%s", err, stdout)
	}
	history := decodeHistory(t, stdout).Data
	withheld, found := findHistoryWithheld(history, "vendor-session")
	if !found {
		t.Fatalf("vendor-session is not reported as withheld: %+v", history.Withheld)
	}
	if withheld.Reason != "out_of_scope" {
		t.Errorf("withheld reason = %q, want out_of_scope", withheld.Reason)
	}

	for label, answer := range map[string]string{"policy": refusal, "history": stdout} {
		if strings.Contains(answer, excludedSecret) {
			t.Errorf("the %s answer carries content from the excluded project:\n%s", label, answer)
		}
	}

	assertTreeUnchanged(t, "excluded repository", repoBefore, snapshotTree(t, excluded))
	assertTreeUnchanged(t, "excluded conversations", conversationsBefore, snapshotTree(t, filepath.Dir(excludedTranscript)))
}
