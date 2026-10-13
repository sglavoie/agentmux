package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The paired release gate installs both programs, restarts the operator's real
// daemon, drives a real tmux, and writes annotated tags. None of that is
// acceptable in a test, and a gate whose logic is only exercised during a
// release is a gate that is discovered to be broken during a release.
//
// So the whole procedure runs here against a stand-in world: two throwaway Git
// repositories, an ephemeral SSH signing key, a fake `just`, a `kitty` that fails, a
// fake `sw_vers`, and a fake ASL wrapper, all under t.TempDir() with the fakes
// staged ahead of everything else on PATH.
//
// What is real matters as much as what is faked. Git is real, so revision
// reading, cleanliness, and annotated tagging are genuinely exercised.
// `ssh-keygen` is real, so the signature this gate produces is verified with the
// same command the documentation tells an operator to run -- under the real
// release namespace, which is the part a mock would have been happy to get
// wrong. Hashing, JSON encoding, and the evidence file are real. Only the
// long-running commands whose behaviour this package does not own are replaced.

// fixture is a complete stand-in world for one gate run.
type fixture struct {
	agentMuxRoot string
	aslRoot      string
	evidenceDir  string
	fakeBin      string
	signingKey   string
	publicKey    string
}

const fakeOSVersion = "26.6"

// harnessCompatPayload is a canned `asl harness-compat --json` report. It is
// shaped like the real one, including a harness the policy does not fully
// endorse, so the evidence assertions cover a mixed report rather than only the
// all-green case.
const harnessCompatPayload = `{
  "schema_version": 1,
  "ok": true,
  "model_use": false,
  "counts": {"supported": 2, "unknown": 1, "incompatible": 0},
  "harnesses": [
    {"harness": "codex", "version": "0.149.1", "status": "supported"},
    {"harness": "copilot", "version": "1.0.99", "status": "unknown"},
    {"harness": "claude", "version": "2.1.231", "status": "supported"}
  ]
}`

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		agentMuxRoot: filepath.Join(base, "agentmux"),
		aslRoot:      filepath.Join(base, "scripts"),
		evidenceDir:  filepath.Join(base, "evidence"),
		fakeBin:      filepath.Join(base, "bin"),
	}
	mkdir(t, f.fakeBin)

	f.stageASLCheckout(t)
	f.stageAgentMuxCheckout(t)
	f.stageFakeCommands(t)
	f.stageSigningKey(t)

	// The fakes go first so they shadow any real `just`, `kitty`, or `sw_vers`
	// on the developer's machine; the rest of PATH is kept because `git` and
	// `ssh-keygen` are deliberately real.
	t.Setenv("PATH", f.fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *fixture) stageASLCheckout(t *testing.T) {
	t.Helper()
	mkdir(t, filepath.Join(f.aslRoot, "agent_session_loop"))
	mkdir(t, filepath.Join(f.aslRoot, "bin", ".local", "bin"))
	mkdir(t, filepath.Join(f.aslRoot, "tests", "fixtures", "agent_mux_contract"))

	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "__init__.py"), "\n")
	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "cli.py"), "def main() -> int:\n    return 0\n")
	// A non-.py sibling that must not participate in the source-tree digest.
	writeFile(t, filepath.Join(f.aslRoot, "agent_session_loop", "notes.txt"), "not source\n")
	writeFile(t, filepath.Join(f.aslRoot, "tests", "fixtures", "agent_mux_contract", "manifest.json"), `{"current": "1"}`+"\n")

	writeScript(t, filepath.Join(f.aslRoot, "bin", ".local", "bin", "agent-session-loop"), `#!/bin/sh
case "$1" in
  harness-compat) cat <<'PAYLOAD'
`+harnessCompatPayload+`
PAYLOAD
  ;;
  *) echo "fake asl: unsupported command $1" >&2; exit 64 ;;
esac
`)
	initRepo(t, f.aslRoot)
}

func (f *fixture) stageAgentMuxCheckout(t *testing.T) {
	t.Helper()
	mkdir(t, f.agentMuxRoot)
	writeFile(t, filepath.Join(f.agentMuxRoot, "main.go"), "package main\n\nfunc main() {}\n")
	initRepo(t, f.agentMuxRoot)
}

// stageFakeCommands writes the stand-ins for the external tools the gate drives
// but does not own. The fake `just` records every recipe it was asked for, which
// is how a test asserts that a phase really did stop rather than merely that the
// record says it did.
func (f *fixture) stageFakeCommands(t *testing.T) {
	t.Helper()
	writeScript(t, filepath.Join(f.fakeBin, "sw_vers"), "#!/bin/sh\necho "+fakeOSVersion+"\n")
	// Kitty is an optional backend: a gate that runs it fails here, as it
	// would on a machine that has none.
	for _, name := range []string{"kitty", "kitten"} {
		writeScript(t, filepath.Join(f.fakeBin, name), "#!/bin/sh\necho 'the release gate must not run "+name+"' >&2\nexit 1\n")
	}
	writeScript(t, filepath.Join(f.fakeBin, "just"), `#!/bin/sh
recipe="$1"
if [ -n "${FAKE_JUST_LOG:-}" ]; then
    echo "$recipe" >> "$FAKE_JUST_LOG"
fi
if [ "$recipe" = "build" ]; then
    # The gate hashes this immediately after the build step, so the fake has to
    # actually produce an artifact.
    printf 'fake amx binary\n' > amx
fi
if [ -n "${FAKE_JUST_FAIL:-}" ] && [ "$recipe" = "$FAKE_JUST_FAIL" ]; then
    echo "fake just: $recipe failed on purpose"
    echo "fake just: diagnostic tail" >&2
    exit 1
fi
echo "fake just: $recipe ok"
`)
}

// stageSigningKey mints an ephemeral, passphrase-free ed25519 key plus the
// allowed-signers file a verifier needs. It never touches ~/.ssh.
func (f *fixture) stageSigningKey(t *testing.T) {
	t.Helper()
	f.signingKey = filepath.Join(t.TempDir(), "release_key")
	run(t, "", "ssh-keygen", "-t", "ed25519", "-N", "", "-C", "amx-release-test", "-f", f.signingKey)

	pub, err := os.ReadFile(f.signingKey + ".pub")
	if err != nil {
		t.Fatalf("read generated public key: %v", err)
	}
	f.publicKey = filepath.Join(filepath.Dir(f.signingKey), "allowed_signers")
	writeFile(t, f.publicKey, "release@test "+strings.TrimSpace(string(pub))+"\n")
}

func (f *fixture) options() Options {
	return Options{
		AgentMuxRoot: f.agentMuxRoot,
		ASLRoot:      f.aslRoot,
		SigningKey:   f.signingKey,
		EvidenceDir:  f.evidenceDir,
		Now:          steppingClock(),
	}
}

// steppingClock advances one second per reading, so every step in a recorded run
// lasts exactly 1000ms. A real clock would make the durations untestable; a
// frozen one would make a duration of zero indistinguishable from a duration
// that was never measured.
func steppingClock() func() time.Time {
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	ticks := 0
	return func() time.Time {
		now := base.Add(time.Duration(ticks) * time.Second)
		ticks++
		return now
	}
}

// isGitTagReadBack identifies the object read that follows an annotated-tag
// write. It must not match `checkTagsAbsent`'s pre-flight probe, which asks the
// same question of the same ref with `--verify --quiet` before anything is
// written -- failing that one would refuse the run instead of reaching the state
// under test.
func isGitTagReadBack(argv []string) bool {
	return len(argv) == 3 && argv[0] == "git" && argv[1] == "rev-parse" &&
		strings.HasPrefix(argv[2], "refs/tags/")
}

// isGitTagDelete identifies deletion by name, the command this package must
// never run and never publish.
func isGitTagDelete(argv []string) bool {
	return len(argv) > 2 && argv[0] == "git" && argv[1] == "tag" && argv[2] == "-d"
}

// isGitTagCreate identifies the annotated-tag write, and only that: the gate
// also runs `git rev-parse refs/tags/...` and `git tag -l` against the same
// repositories, and an injected failure that caught those would be testing
// something else.
func isGitTagCreate(argv []string) bool {
	return len(argv) > 2 && argv[0] == "git" && argv[1] == "tag" && argv[2] == "-a"
}

func isGitUpdateRef(argv []string) bool {
	return len(argv) > 1 && argv[0] == "git" && argv[1] == "update-ref"
}

// isSSHKeygenSign identifies the signing invocation. `ssh-keygen` is also the
// command the fixture mints its key with, and the gate never runs that one, but
// matching on the subcommand keeps the injection about signing rather than about
// the executable's name.
func isSSHKeygenSign(argv []string) bool {
	return len(argv) > 2 && argv[0] == "ssh-keygen" && argv[1] == "-Y" && argv[2] == "sign"
}

func commandsMatching(commands [][]string, pred func([]string) bool) [][]string {
	var matched [][]string
	for _, argv := range commands {
		if pred(argv) {
			matched = append(matched, argv)
		}
	}
	return matched
}

// --- helpers -------------------------------------------------------------

func verifySignature(t *testing.T, argv []string, evidencePath string) {
	t.Helper()
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("read evidence for verification: %v", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`%s` failed: %v\n%s", strings.Join(argv, " "), err, out)
	}
}

// requireNoValidSignature is verifySignature's opposite, and it runs the same
// command an operator would rather than inspecting the filesystem: a retracted
// record is only really retracted if `ssh-keygen -Y verify` refuses the bytes
// that are on disk now. Whether the signature file was removed or emptied is an
// implementation detail; being unable to vouch for the record is not.
func requireNoValidSignature(t *testing.T, argv []string, evidencePath string) {
	t.Helper()
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("read evidence for verification: %v", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("`%s` still vouches for a record whose release was retracted:\n%s",
			strings.Join(argv, " "), out)
	}
}

func artifactHash(t *testing.T, ev *Evidence, name string) string {
	t.Helper()
	for _, artifact := range ev.Artifacts {
		if artifact.Name == name {
			return artifact.SHA256
		}
	}
	t.Fatalf("no artifact named %q in %+v", name, ev.Artifacts)
	return ""
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	sum, err := sha256File(path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return sum
}

func revisionOf(t *testing.T, root string) string {
	t.Helper()
	return strings.TrimSpace(run(t, root, "git", "rev-parse", "HEAD"))
}

func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

// initRepo makes a real Git repository with one commit. The identity and
// signing settings are set locally so the test never depends on -- or is broken
// by -- the developer's global Git configuration.
func initRepo(t *testing.T, root string) {
	t.Helper()
	run(t, root, "git", "init", "--initial-branch=main")
	run(t, root, "git", "config", "user.email", "release@test")
	run(t, root, "git", "config", "user.name", "Release Test")
	run(t, root, "git", "config", "commit.gpgsign", "false")
	run(t, root, "git", "config", "tag.gpgsign", "false")
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-m", "fixture")
	// The gate must ignore the artifact it builds; leaving it untracked would
	// make every run dirty.
	writeFile(t, filepath.Join(root, ".gitignore"), "amx\n")
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-m", "ignore build output")
}

func run(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`%s` in %s failed: %v\n%s", strings.Join(argv, " "), dir, err, out)
	}
	return string(out)
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeScript(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write script %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
