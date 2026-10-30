package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git is real, including the pushes to bare repositories. Only GitHub,
// Apple tooling and the expensive gates are replaced, so these tests cannot
// publish a release or touch an installed daemon.
type shipWorld struct {
	root, asl, bin, trace, key string
}

func shipWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func shipGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newShipWorld(t *testing.T) shipWorld {
	t.Helper()
	base := t.TempDir()
	w := shipWorld{
		root: filepath.Join(base, "agent mux"),
		asl:  filepath.Join(base, "sglavoie_agent-session-loop"),
		bin:  filepath.Join(base, "bin"), trace: filepath.Join(base, "trace"),
		key: filepath.Join(base, "release key"),
	}
	shipWrite(t, w.key, "test-only signing key", 0o600)
	for _, name := range []string{"ship-release", "publish-release", "named-arg"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		shipWrite(t, filepath.Join(w.root, "scripts", name), string(data), 0o755)
	}
	shipWrite(t, filepath.Join(w.root, "cmd/root.go"), "package cmd\n\nconst version = \"0.8.9\"\n", 0o644)
	shipWrite(t, filepath.Join(w.root, "Justfile"), "# gates replaced by a stand-in\n", 0o644)
	shipWrite(t, filepath.Join(w.root, "macos/scripts/release-gate.sh"), `#!/bin/sh
echo toolchain >> "$SHIP_TRACE"
exit "${SHIP_TOOLCHAIN_STATUS:-0}"
`, 0o755)
	shipWrite(t, filepath.Join(w.bin, "gh"), `#!/bin/sh
echo "gh $*" >> "$SHIP_TRACE"
case "$1 $2" in
    'auth status') exit 0 ;;
    'repo view') echo test/agentmux ;;
    'release list') echo "${SHIP_EXISTING_RELEASE:-}"; exit "${SHIP_GITHUB_STATUS:-0}" ;;
    'run list') echo 42 ;;
    'run watch') exit "${SHIP_CI_STATUS:-0}" ;;
    *) echo "unexpected gh command: $*" >&2; exit 1 ;;
esac
`, 0o755)
	shipWrite(t, filepath.Join(w.bin, "just"), `#!/bin/sh
printf 'gate' >> "$SHIP_TRACE"
printf ' <%s>' "$@" >> "$SHIP_TRACE"
printf '\n' >> "$SHIP_TRACE"
exit "${SHIP_GATE_STATUS:-0}"
`, 0o755)
	for _, root := range []string{w.root, w.asl} {
		shipWrite(t, filepath.Join(root, "fixture"), "fixture\n", 0o644)
		shipGit(t, base, "init", "--bare", root+".git")
		shipGit(t, root, "init", "-b", "main")
		shipGit(t, root, "config", "user.name", "Release Test")
		shipGit(t, root, "config", "user.email", "release@example.invalid")
		shipGit(t, root, "config", "commit.gpgsign", "false")
		shipGit(t, root, "config", "core.hooksPath", filepath.Join(base, "empty-hooks"))
		shipGit(t, root, "add", ".")
		shipGit(t, root, "commit", "-m", "initial")
		shipGit(t, root, "remote", "add", "origin", root+".git")
		shipGit(t, root, "push", "-u", "origin", "main")
	}
	t.Setenv("PATH", w.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHIP_TRACE", w.trace)
	t.Setenv("AMX_RELEASE_SIGNING_KEY", w.key)
	t.Setenv("AMX_ASL_ROOT", w.asl)
	t.Setenv("AMX_CODESIGN_IDENTITY", "test-developer-id")
	t.Setenv("AMX_NOTARY_PROFILE", "test-notary")
	return w
}

func (w shipWorld) run(args ...string) (string, error) {
	cmd := exec.Command("bash", append([]string{filepath.Join(w.root, "scripts/ship-release")}, args...)...)
	cmd.Dir = w.root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestShipBumpsPushesAndWaitsBeforeGates(t *testing.T) {
	for _, tc := range []struct{ bump, want string }{
		{"patch", "0.8.10"}, {"minor", "0.9.0"}, {"major", "1.0.0"},
	} {
		t.Run(tc.bump, func(t *testing.T) {
			w := newShipWorld(t)
			// Local work on main may be pushed together with the version commit.
			shipGit(t, w.root, "commit", "--allow-empty", "-m", "ready to release")
			out, err := w.run(tc.bump)
			if err != nil {
				t.Fatalf("ship: %v\n%s", err, out)
			}
			if got := shipGit(t, w.root, "log", "-1", "--format=%s"); got != "chore(release): amx "+tc.want {
				t.Fatalf("version commit = %q", got)
			}
			if got := shipGit(t, w.root, "show", "origin/main:cmd/root.go"); !strings.Contains(got, `const version = "`+tc.want+`"`) {
				t.Fatalf("pushed version: %s", got)
			}
			if got := shipGit(t, w.root, "status", "--porcelain"); got != "" {
				t.Fatalf("dirty tree: %s", got)
			}
			trace, _ := os.ReadFile(w.trace)
			s := string(trace)
			rev := shipGit(t, w.root, "rev-parse", "HEAD")
			if !strings.Contains(s, "--commit "+rev+" --branch main --event push") {
				t.Fatalf("did not select the exact push's CI: %s", s)
			}
			if i, j := strings.Index(s, "gh run watch 42"), strings.Index(s, "gate <"); i < 0 || j < i {
				t.Fatalf("gates ran before CI: %s", s)
			}
			if !strings.Contains(s, "<release> <"+w.asl+"> <"+w.key+">") {
				t.Fatalf("gate paths were not preserved: %s", s)
			}
		})
	}
}

func TestShipDryRunDoesNotCommitPushOrRunGates(t *testing.T) {
	w := newShipWorld(t)
	t.Setenv("AMX_ASL_ROOT", "") // Exercise the sibling checkout default.
	before := shipGit(t, w.root, "rev-parse", "HEAD")
	out, err := w.run("patch", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if shipGit(t, w.root, "rev-parse", "HEAD") != before || shipGit(t, w.root, "status", "--porcelain") != "" {
		t.Fatal("dry run changed the source")
	}
	trace, _ := os.ReadFile(w.trace)
	if !strings.Contains(string(trace), "toolchain") || strings.Contains(string(trace), "gh run") || strings.Contains(string(trace), "gate <") {
		t.Fatalf("unexpected dry run operations: %s", trace)
	}
}

func TestShipPreflightFailuresLeaveSourceUntouched(t *testing.T) {
	for _, failure := range []string{"dirty", "branch", "behind", "key", "asl", "unpushed-asl", "duplicate", "github", "toolchain", "invalid-argument"} {
		t.Run(failure, func(t *testing.T) {
			w := newShipWorld(t)
			args := []string{"patch"}
			switch failure {
			case "dirty":
				shipWrite(t, filepath.Join(w.root, "untracked"), "dirty", 0o644)
			case "branch":
				shipGit(t, w.root, "checkout", "-b", "feature")
			case "behind":
				shipGit(t, w.root, "commit", "--allow-empty", "-m", "remote work")
				shipGit(t, w.root, "push", "origin", "main")
				shipGit(t, w.root, "reset", "--hard", "HEAD~1")
			case "key":
				t.Setenv("AMX_RELEASE_SIGNING_KEY", w.key+"-missing")
			case "asl":
				shipWrite(t, filepath.Join(w.asl, "untracked"), "dirty", 0o644)
			case "unpushed-asl":
				shipGit(t, w.asl, "commit", "--allow-empty", "-m", "unpushed peer")
			case "duplicate":
				t.Setenv("SHIP_EXISTING_RELEASE", "amx 0.8.10")
			case "github":
				t.Setenv("SHIP_GITHUB_STATUS", "1")
			case "toolchain":
				t.Setenv("SHIP_TOOLCHAIN_STATUS", "75")
			case "invalid-argument":
				args = []string{"patch", "--unknown"}
			}
			before := shipGit(t, w.root, "rev-parse", "HEAD")
			if out, err := w.run(args...); err == nil {
				t.Fatalf("expected refusal, got: %s", out)
			}
			if shipGit(t, w.root, "rev-parse", "HEAD") != before || strings.Contains(shipGit(t, w.root, "diff", "--", "cmd/root.go"), "const version") {
				t.Fatal("failed preflight changed the version")
			}
		})
	}
}

func TestShipFailuresPreserveVersionAndExplainRecovery(t *testing.T) {
	for _, failure := range []string{"push", "ci", "gates"} {
		t.Run(failure, func(t *testing.T) {
			w := newShipWorld(t)
			switch failure {
			case "push":
				shipWrite(t, filepath.Join(w.root+".git", "hooks/pre-receive"), "#!/bin/sh\nexit 1\n", 0o755)
				shipGit(t, w.root+".git", "config", "core.hooksPath", "hooks")
			case "ci":
				t.Setenv("SHIP_CI_STATUS", "1")
			case "gates":
				t.Setenv("SHIP_GATE_STATUS", "1")
			}
			out, err := w.run("patch")
			if err == nil || !strings.Contains(out, "Do not bump again") {
				t.Fatalf("missing recovery instructions: %v\n%s", err, out)
			}
			if got := shipGit(t, w.root, "log", "-1", "--format=%s"); got != "chore(release): amx 0.8.10" {
				t.Fatalf("version commit not preserved: %s", got)
			}
			trace, _ := os.ReadFile(w.trace)
			if failure != "gates" && strings.Contains(string(trace), "gate <") {
				t.Fatalf("gates ran despite failed push or CI: %s", trace)
			}
			if failure == "gates" && !strings.Contains(out, "#retrying-a-release") {
				t.Fatalf("missing evidence recovery instructions: %s", out)
			}
		})
	}
}
