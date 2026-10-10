package tmux

// Which tmux executable the private server runs. The desktop app ships a
// pinned runtime; a standalone amx uses a supported tmux it finds installed.
// Either way a running server is never handed a different executable: the one
// that started it is recorded beside its socket and used until it exits.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/atomicfile"
)

// Environment variables that steer the resolution.
const (
	// BinaryEnv names a tmux executable to use instead of any other.
	BinaryEnv = "AMX_TMUX"
	// RuntimeEnv names the directory of a bundled runtime. Agent Mux.app sets
	// it to the runtime inside its bundle for every amx it runs.
	RuntimeEnv = "AMX_TMUX_RUNTIME"
)

// MinVersion is the oldest tmux release the private server's configuration
// works with: `extended-keys-format` arrived in 3.5.
const MinVersion = "3.5"

// RuntimeSource says where a tmux executable came from.
type RuntimeSource string

const (
	// SourceConfigured is `[defaults.tmux] path` in config.toml.
	SourceConfigured RuntimeSource = "config"
	// SourceEnvironment is AMX_TMUX.
	SourceEnvironment RuntimeSource = "environment"
	// SourceBundled is the runtime shipped with Agent Mux.app, verified
	// against its checksums and installed under the data directory.
	SourceBundled RuntimeSource = "bundled"
	// SourcePath is tmux on PATH.
	SourcePath RuntimeSource = "path"
	// SourceInstalled is a bundled runtime an earlier run installed, used
	// when this amx was started without the app and no tmux is on PATH.
	SourceInstalled RuntimeSource = "installed"
	// SourceServer is the executable the running server was started with.
	SourceServer RuntimeSource = "server"
)

// Runtime is a tmux executable the private server can run.
type Runtime struct {
	Binary  string        `json:"path"`
	Version string        `json:"version"`
	Source  RuntimeSource `json:"source"`
	// Verified reports that every file of a bundled runtime matched its
	// recorded checksum when it was resolved.
	Verified bool `json:"verified"`
}

// Candidate is one place the resolution looked.
type Candidate struct {
	Source  RuntimeSource `json:"source"`
	Path    string        `json:"path,omitempty"`
	Version string        `json:"version,omitempty"`
	// Problem is why this candidate cannot be used; empty for a usable one.
	Problem string `json:"problem,omitempty"`
}

// RuntimeOptions configures a resolution. Zero values read the process: its
// environment, its executable, and the default data directory.
type RuntimeOptions struct {
	// Configured is `[defaults.tmux] path`; empty when the operator set none.
	Configured string
	// Getenv reads the environment. Default: os.Getenv.
	Getenv func(string) string
	// Executable is this program's path, beside which a bundled runtime is
	// looked for. Default: os.Executable.
	Executable string
	// Root is where bundled runtimes are installed. Default:
	// DefaultRuntimeRoot().
	Root string
	// LookPath finds tmux on PATH. Default: exec.LookPath.
	LookPath func(string) (string, error)
	// Version reports a tmux executable's release. Default: runs `tmux -V`.
	Version func(binary string) (string, error)
}

// configured is `[defaults.tmux] path`, set once by the command line after it
// loads the configuration. Code that opens a server without a configuration
// at hand (the daemon reconciling a server a record names) reads it too.
var configured string

// SetConfiguredBinary records `[defaults.tmux] path` for every later
// resolution in this process.
func SetConfiguredBinary(path string) { configured = strings.TrimSpace(path) }

// DefaultRuntimeRoot is where bundled runtimes are installed, one directory
// per build. It is outside the app so that replacing or removing the app
// never touches the executable a running server was started from.
func DefaultRuntimeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "agent-mux", "runtime")
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "runtime")
}

// NoRuntimeError reports that no usable tmux was found, with every place
// that was tried and what to do about it.
type NoRuntimeError struct {
	Candidates []Candidate
}

func (e *NoRuntimeError) Error() string {
	var b strings.Builder
	b.WriteString("no supported tmux found (need ")
	b.WriteString(MinVersion)
	b.WriteString(" or newer):")
	for _, c := range e.Candidates {
		b.WriteString("\n  ")
		b.WriteString(c.describe())
	}
	b.WriteString("\n")
	b.WriteString(RuntimeHint)
	return b.String()
}

// RuntimeHint is how an operator gets a usable tmux.
const RuntimeHint = "Install tmux " + MinVersion + " or newer (`brew install tmux`), set [defaults.tmux] path in config.toml, or run Agent Mux.app, which bundles one."

func (c Candidate) describe() string {
	where := map[RuntimeSource]string{
		SourceConfigured:  "[defaults.tmux] path",
		SourceEnvironment: BinaryEnv,
		SourceBundled:     "bundled runtime",
		SourcePath:        "PATH",
		SourceInstalled:   "installed runtime",
		SourceServer:      "running server",
	}[c.Source]
	if c.Path != "" {
		where += " (" + c.Path + ")"
	}
	if c.Problem == "" {
		return where + ": tmux " + c.Version
	}
	return where + ": " + c.Problem
}

// ResolveRuntime picks the tmux executable a new private server would run.
// An explicit choice (the configuration, then AMX_TMUX) is final: when it
// cannot be used nothing else is tried, so a typo never silently runs another
// tmux. Otherwise the first usable of the bundled runtime, tmux on PATH, and
// a previously installed runtime wins. The candidates are returned either
// way, for diagnostics.
func ResolveRuntime(opts RuntimeOptions) (Runtime, []Candidate, error) {
	opts = opts.withDefaults()
	var candidates []Candidate
	explicit := func(source RuntimeSource, path string) (Runtime, []Candidate, error) {
		rt, c := opts.inspect(source, path)
		candidates = append(candidates, c)
		if c.Problem != "" {
			return Runtime{}, candidates, &NoRuntimeError{Candidates: candidates}
		}
		return rt, candidates, nil
	}
	if path := strings.TrimSpace(opts.Configured); path != "" {
		return explicit(SourceConfigured, path)
	}
	if path := strings.TrimSpace(opts.Getenv(BinaryEnv)); path != "" {
		return explicit(SourceEnvironment, path)
	}

	var chosen *Runtime
	consider := func(rt Runtime, c Candidate) {
		candidates = append(candidates, c)
		if c.Problem == "" && chosen == nil {
			chosen = &rt
		}
	}
	if dir := opts.bundleDir(); dir != "" {
		consider(opts.installBundle(dir))
	} else {
		candidates = append(candidates, Candidate{Source: SourceBundled, Problem: "not present (this amx was not started by Agent Mux.app)"})
	}
	if path, err := opts.LookPath("tmux"); err != nil {
		candidates = append(candidates, Candidate{Source: SourcePath, Problem: "tmux not found"})
	} else {
		consider(opts.inspect(SourcePath, path))
	}
	if chosen == nil {
		if dir := opts.currentInstall(); dir != "" {
			consider(opts.verifyInstalled(SourceInstalled, dir))
		}
	}
	if chosen == nil {
		return Runtime{}, candidates, &NoRuntimeError{Candidates: candidates}
	}
	return *chosen, candidates, nil
}

func (o RuntimeOptions) withDefaults() RuntimeOptions {
	if o.Configured == "" {
		o.Configured = configured
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Executable == "" {
		if exe, err := os.Executable(); err == nil {
			o.Executable = exe
		}
	}
	if o.Root == "" {
		o.Root = DefaultRuntimeRoot()
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Version == nil {
		o.Version = binaryVersion
		if binaryVersionForTest != nil {
			o.Version = binaryVersionForTest
		}
	}
	return o
}

// inspect judges one tmux executable by the release it reports.
func (o RuntimeOptions) inspect(source RuntimeSource, path string) (Runtime, Candidate) {
	c := Candidate{Source: source, Path: path}
	if !filepath.IsAbs(path) {
		c.Problem = "not an absolute path"
		return Runtime{}, c
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		c.Problem = "not an executable file"
		return Runtime{}, c
	}
	version, err := o.Version(path)
	if err != nil {
		c.Problem = err.Error()
		return Runtime{}, c
	}
	c.Version = version
	if !SupportedVersion(version) {
		c.Problem = "tmux " + version + " is older than " + MinVersion
		return Runtime{}, c
	}
	return Runtime{Binary: path, Version: version, Source: source}, c
}

// bundleDir is the bundled runtime this process was given: the directory
// AMX_TMUX_RUNTIME names, else `tmux` beside the directory holding this
// executable, which is where Agent Mux.app keeps it next to its amx
// (Contents/Resources/bin/amx and Contents/Resources/tmux).
func (o RuntimeOptions) bundleDir() string {
	if dir := strings.TrimSpace(o.Getenv(RuntimeEnv)); dir != "" {
		return dir
	}
	if o.Executable == "" {
		return ""
	}
	exe := o.Executable
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Join(filepath.Dir(filepath.Dir(exe)), "tmux")
	if _, err := os.Stat(filepath.Join(dir, checksumsName)); err != nil {
		return ""
	}
	return dir
}

// The files of a bundled runtime.
const (
	checksumsName = "SHA256SUMS"
	versionName   = "VERSION"
	currentName   = "current"
	bundledBinary = "bin/tmux"
)

// installBundle verifies the bundled runtime in dir against its checksums and
// copies it under the runtime root, in a directory named after its version
// and checksums. A build already installed is verified in place and reused.
// The copy is what runs: a server started from it keeps its executable
// whatever happens to the app bundle afterwards.
func (o RuntimeOptions) installBundle(dir string) (Runtime, Candidate) {
	c := Candidate{Source: SourceBundled, Path: dir}
	fail := func(format string, args ...any) (Runtime, Candidate) {
		c.Problem = fmt.Sprintf(format, args...)
		return Runtime{}, c
	}
	if !filepath.IsAbs(dir) {
		return fail("not an absolute path")
	}
	sums, err := os.ReadFile(filepath.Join(dir, checksumsName))
	if err != nil {
		return fail("no checksums: %v", err)
	}
	files, err := parseChecksums(sums)
	if err != nil {
		return fail("%v", err)
	}
	version, err := bundleVersion(dir)
	if err != nil {
		return fail("%v", err)
	}
	c.Version = version
	if !SupportedVersion(version) {
		return fail("tmux %s is older than %s", version, MinVersion)
	}
	digest := sha256.Sum256(sums)
	target := filepath.Join(o.Root, "tmux-"+version+"-"+hex.EncodeToString(digest[:8]))

	// A copy that is missing, partial, or altered is installed again from the
	// bundle; the directory it replaces is renamed aside, never written over.
	installed, err := os.ReadFile(filepath.Join(target, checksumsName))
	if err != nil || !bytes.Equal(installed, sums) || verifyFiles(target, files) != nil {
		if err := verifyFiles(dir, files); err != nil {
			return fail("%v", err)
		}
		if err := copyRuntime(dir, target, files, sums); err != nil {
			return fail("installing into %s: %v", target, err)
		}
		if err := verifyFiles(target, files); err != nil {
			return fail("installed copy %s: %v", target, err)
		}
	}
	// Remembered for an amx that is later started without the app.
	if err := atomicfile.Write(filepath.Join(o.Root, currentName), []byte(filepath.Base(target)+"\n"), 0o600); err != nil {
		return fail("recording %s: %v", target, err)
	}
	c.Path = filepath.Join(target, bundledBinary)
	return Runtime{Binary: c.Path, Version: version, Source: SourceBundled, Verified: true}, c
}

// currentInstall is the runtime directory the last installBundle recorded,
// or empty.
func (o RuntimeOptions) currentInstall() string {
	name, err := os.ReadFile(filepath.Join(o.Root, currentName))
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(string(name))
	if base == "" || base != filepath.Base(base) || !strings.HasPrefix(base, "tmux-") {
		return ""
	}
	return filepath.Join(o.Root, base)
}

// verifyInstalled checks a runtime directory under the root against the
// checksums installed with it.
func (o RuntimeOptions) verifyInstalled(source RuntimeSource, dir string) (Runtime, Candidate) {
	c := Candidate{Source: source, Path: filepath.Join(dir, bundledBinary)}
	sums, err := os.ReadFile(filepath.Join(dir, checksumsName))
	if err == nil {
		var files []checksum
		if files, err = parseChecksums(sums); err == nil {
			err = verifyFiles(dir, files)
		}
	}
	var version string
	if err == nil {
		version, err = bundleVersion(dir)
	}
	if err != nil {
		c.Problem = err.Error()
		return Runtime{}, c
	}
	c.Version = version
	return Runtime{Binary: c.Path, Version: version, Source: source, Verified: true}, c
}

type checksum struct {
	sum  string
	path string
}

// parseChecksums reads `shasum -a 256` output: one "<hex>  <path>" per line,
// paths relative to the runtime directory. The executable must be listed.
func parseChecksums(data []byte) ([]checksum, error) {
	var files []checksum
	seen := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum, path, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != sha256.Size*2 {
			return nil, fmt.Errorf("malformed checksum line %q", line)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("malformed checksum line %q", line)
		}
		clean := filepath.Clean(path)
		if path == "" || filepath.IsAbs(path) || clean != path || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("checksum names a path outside the runtime: %q", path)
		}
		seen = seen || path == bundledBinary
		files = append(files, checksum{sum: strings.ToLower(sum), path: path})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !seen {
		return nil, fmt.Errorf("checksums do not list %s", bundledBinary)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

func verifyFiles(dir string, files []checksum) error {
	for _, f := range files {
		got, err := fileSHA256(filepath.Join(dir, f.path))
		if err != nil {
			return fmt.Errorf("%s: %w", f.path, err)
		}
		if got != f.sum {
			return fmt.Errorf("%s does not match its checksum (got %s, recorded %s)", f.path, got, f.sum)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- a file of a runtime directory this process resolved.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// bundleVersion reads the tmux release from a runtime directory's VERSION.
func bundleVersion(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, versionName))
	if err != nil {
		return "", fmt.Errorf("no %s: %w", versionName, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok && key == "tmux" {
			value = strings.TrimSpace(value)
			if value == "" || strings.ContainsAny(value, "/ \t") {
				break
			}
			return value, nil
		}
	}
	return "", fmt.Errorf("%s names no tmux release", versionName)
}

// copyRuntime copies the checked files into a fresh directory beside target
// and renames it into place, so a reader never sees a partial runtime. A
// target left by an interrupted or tampered install is replaced only when no
// file in it is in use as it stands, which a rename of the directory
// guarantees: a server running the old copy keeps its open executable.
func copyRuntime(from, target string, files []checksum, sums []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	listed := false
	for _, f := range files {
		listed = listed || f.path == versionName
		if err := copyFile(filepath.Join(from, f.path), filepath.Join(staging, f.path)); err != nil {
			return err
		}
	}
	// VERSION names the release the directory is for; a bundle that does not
	// list it among its checksums still carries it.
	if !listed {
		if err := copyFile(filepath.Join(from, versionName), filepath.Join(staging, versionName)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(staging, checksumsName), sums, 0o600); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		retired := target + ".retired-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := os.Rename(target, retired); err != nil {
			return err
		}
	}
	return os.Rename(staging, target)
}

func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	in, err := os.Open(from) // #nosec G304 -- a checksummed file of the bundled runtime.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info.Mode().Perm()&0o111 != 0 {
		mode = 0o700
	}
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G304 -- inside a staging directory this process created.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// binaryVersionForTest stands in for running `tmux -V` in this package's tests.
var binaryVersionForTest func(string) (string, error)

// binaryVersion asks a tmux executable for its release: `tmux -V` prints
// "tmux 3.7c".
func binaryVersion(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "-V").Output() // #nosec G204 -- a resolved tmux executable, fixed argument.
	if err != nil {
		return "", fmt.Errorf("`tmux -V` failed: %w", err)
	}
	name, version, ok := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !ok || name != "tmux" || version == "" {
		return "", fmt.Errorf("`tmux -V` printed %q", strings.TrimSpace(string(out)))
	}
	return version, nil
}

// SupportedVersion reports whether a tmux release is MinVersion or newer.
// Releases are "3.5", "3.7c", or "next-3.8" for a development build; anything
// else cannot be judged and is refused.
func SupportedVersion(version string) bool {
	major, minor, ok := parseVersion(version)
	if !ok {
		return false
	}
	wantMajor, wantMinor, _ := parseVersion(MinVersion)
	return major > wantMajor || (major == wantMajor && minor >= wantMinor)
}

func parseVersion(version string) (major, minor int, ok bool) {
	version = strings.TrimPrefix(version, "next-")
	head, tail, found := strings.Cut(version, ".")
	if !found {
		return 0, 0, false
	}
	major, err := strconv.Atoi(head)
	if err != nil {
		return 0, 0, false
	}
	digits := 0
	for digits < len(tail) && tail[digits] >= '0' && tail[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(tail[:digits])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// --- The executable of a running server -----------------------------------------

// pinName is the file beside the socket naming the executable the server was
// started with.
const pinName = "tmux.binary"

// Listening reports whether a server accepts connections on socket. It needs
// no tmux executable, so it can be asked before one is chosen.
func Listening(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// PinnedBinary is the executable recorded for the server in dir, or empty
// when none is recorded or the record no longer names an executable file.
func PinnedBinary(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, pinName))
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(data))
	if !filepath.IsAbs(path) {
		return ""
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return path
}

// serverBinary chooses the executable for the server in dir. A server that is
// running keeps the one it was started with, whatever would be resolved now:
// an app update that ships another tmux must not put a different client, let
// alone a different server, under live sessions. Only when no server is
// running is the resolution asked, and its answer recorded for the server the
// next launch starts.
func serverBinary(dir, socket string) (string, error) {
	if Listening(socket) {
		if pinned := PinnedBinary(dir); pinned != "" {
			return pinned, nil
		}
	}
	rt, _, err := ResolveRuntime(RuntimeOptions{})
	if err != nil {
		return "", err
	}
	if PinnedBinary(dir) != rt.Binary {
		if err := atomicfile.Write(filepath.Join(dir, pinName), []byte(rt.Binary+"\n"), 0o600); err != nil {
			return "", fmt.Errorf("tmux: recording the server executable: %w", err)
		}
	}
	return rt.Binary, nil
}

// IsNoRuntime reports whether err says that no usable tmux was found.
func IsNoRuntime(err error) bool {
	var none *NoRuntimeError
	return errors.As(err, &none)
}
