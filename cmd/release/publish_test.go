package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the real publisher against real Git repositories and signed evidence.
// GitHub and Apple tooling stay behind fixtures: no external release is made.
func TestPublishUploadsChecksumsOfSignedDownloads(t *testing.T) {
	for _, tool := range []string{"jq", "shasum", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	w := newShipWorld(t)
	const pairID = "amx-asl-1.download-fixture"
	for _, root := range []string{w.root, w.asl} {
		shipGit(t, root, "tag", pairID)
	}
	revision := shipGit(t, w.root, "rev-parse", "HEAD")
	unsigned := []byte("unsigned amx build\n")
	shipWrite(t, filepath.Join(w.root, "amx"), string(unsigned), 0o755)
	appExecutable := filepath.Join(w.root, "macos/build/Agent Mux.app/Contents/MacOS/Agent Mux")
	shipWrite(t, appExecutable, "app executable\n", 0o755)
	hash := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	key := filepath.Join(t.TempDir(), "evidence-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generate evidence key: %v\n%s", err, out)
	}
	writeRecord := func(name, namespace string, data any) string {
		t.Helper()
		body, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), name)
		shipWrite(t, path, string(body), 0o644)
		if out, err := exec.Command("ssh-keygen", "-q", "-Y", "sign", "-f", key, "-n", namespace, path).CombinedOutput(); err != nil {
			t.Fatalf("sign evidence: %v\n%s", err, out)
		}
		return path
	}
	pair := writeRecord("pair.json", "agentmux-asl-release", map[string]any{
		"result": "passed", "signature": map[string]bool{"signed": true}, "pair_id": pairID,
		"tags": []string{pairID, pairID}, "environment": map[string]string{"arch": "arm64"},
		"contract": map[string]string{"set_id": "fixture"},
		"repositories": []map[string]string{
			{"repo": "agentmux", "root": w.root, "revision": revision},
			{"repo": "agent-session-loop", "root": w.asl, "revision": shipGit(t, w.asl, "rev-parse", "HEAD")},
		},
		"artifacts": []map[string]string{{"name": "amx", "sha256": hash(unsigned)}},
	})
	desktop := writeRecord("desktop.json", "agentmux-desktop-release", map[string]any{
		"result": "passed", "signature": map[string]bool{"signed": true}, "gate": "desktop",
		"rehearsal": false, "notarized_required": true,
		"repository": map[string]string{"revision": revision},
		"bundle": []map[string]string{
			{"name": "distribution", "value": "notarized"},
			{"name": "amx_go_build_id", "value": "fixture-build-id"},
			{"name": "bundle_executable_sha256", "value": hash([]byte("app executable\n"))},
		},
	})
	shipWrite(t, filepath.Join(w.bin, "go"), "#!/bin/sh\necho fixture-build-id\n", 0o755)
	shipWrite(t, filepath.Join(w.bin, "spctl"), "#!/bin/sh\nexit 0\n", 0o755)
	shipWrite(t, filepath.Join(w.bin, "xcrun"), "#!/bin/sh\necho 'status: Accepted'\n", 0o755)
	shipWrite(t, filepath.Join(w.bin, "codesign"), `#!/bin/sh
for target do :; done
printf 'fixture signature\n' >> "$target"
`, 0o755)
	shipWrite(t, filepath.Join(w.bin, "ditto"), `#!/bin/sh
source=; destination=
for arg do source=$destination; destination=$arg; done
if [ -f "$source/amx" ]; then
    cat "$source/amx" > "$destination"
else
    printf 'fixture app archive\n' > "$destination"
fi
`, 0o755)
	shipWrite(t, filepath.Join(w.bin, "gh"), `#!/bin/sh
case "$1 $2" in
    'repo view') echo test/agentmux ;;
    'release view')
        case "$*" in *--json*) echo https://example.invalid/release ;; *) exit "$PUBLISH_VIEW_STATUS" ;; esac ;;
    'release create'|'release upload')
        printf '%s\n' "$2" > "$PUBLISH_UPLOAD/method"
        for arg do
            case "$arg" in
                *.zip|*.json|*.sig|*/SHA256SUMS|*/notes.md) cp "$arg" "$PUBLISH_UPLOAD/" ;;
            esac
        done ;;
    *) echo "unexpected gh command: $*" >&2; exit 1 ;;
esac
`, 0o755)
	for _, method := range []string{"create", "upload"} {
		t.Run(method, func(t *testing.T) {
			upload := t.TempDir()
			t.Setenv("PUBLISH_UPLOAD", upload)
			status := "1"
			if method == "upload" {
				status = "0"
			}
			t.Setenv("PUBLISH_VIEW_STATUS", status)
			cmd := exec.Command(filepath.Join(w.root, "scripts/publish-release"), "publish", w.root, pair, desktop)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("publish: %v\n%s", err, out)
			}
			read := func(name string) []byte {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(upload, name))
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			if got := strings.TrimSpace(string(read("method"))); got != method {
				t.Fatalf("publisher used %q, want %q", got, method)
			}
			checksums := map[string]string{}
			for _, line := range strings.Split(strings.TrimSpace(string(read("SHA256SUMS"))), "\n") {
				fields := strings.Fields(line)
				if len(fields) != 2 {
					t.Fatalf("invalid checksum row %q", line)
				}
				checksums[fields[1]] = fields[0]
			}
			names := []string{"Agent-Mux-0.8.9-macos-arm64.zip", "amx-0.8.9-darwin-arm64.zip", "pair.json", "pair.json.sig", "desktop.json", "desktop.json.sig"}
			if len(checksums) != len(names) {
				t.Fatalf("checksums cover %v, want %v", checksums, names)
			}
			for _, name := range names {
				if got, want := checksums[name], hash(read(name)); got != want {
					t.Errorf("uploaded %s checksum = %q, actual bytes = %q", name, got, want)
				}
			}
			if checksums["amx-0.8.9-darwin-arm64.zip"] == hash(unsigned) {
				t.Fatal("download checksum describes the unsigned build instead of the signed download")
			}
			if method == "create" {
				notes := string(read("notes.md"))
				if !strings.Contains(notes, "Set Up First Project") || !strings.Contains(notes, "--ignore-missing SHA256SUMS") {
					t.Fatal("release notes omit the first-session or checksum instructions")
				}
			}
		})
	}
}
