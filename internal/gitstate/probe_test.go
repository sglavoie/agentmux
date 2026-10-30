package gitstate

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// gitCall records one invocation the injected runner saw.
type gitCall struct {
	directory string
	timeout   time.Duration
	args      []string
}

// scriptedGit answers each command by its first argument. A missing entry is an
// error, which is how "git could not answer that" is spelled everywhere here.
type scriptedGit struct {
	revParse    []byte
	revParseErr error
	status      []byte
	statusErr   error
	err         error

	calls []gitCall
}

func (s *scriptedGit) run(_ context.Context, directory string, timeout time.Duration, args ...string) ([]byte, error) {
	s.calls = append(s.calls, gitCall{directory: directory, timeout: timeout, args: args})
	if s.err != nil {
		return nil, s.err
	}
	switch args[0] {
	case "rev-parse":
		return s.revParse, s.revParseErr
	case "status":
		return s.status, s.statusErr
	default:
		return nil, errors.New("unexpected git command")
	}
}

func TestProbeTable(t *testing.T) {
	t.Parallel()

	timeout := errors.New("git rev-parse timed out after 2s")
	missingBinary := &exec.Error{Name: "git", Err: exec.ErrNotFound}
	notARepo := errors.New("exit status 128")

	tests := []struct {
		name string
		git  scriptedGit
		want State
	}{
		{
			name: "clean repository reports a branch and no dirt",
			git:  scriptedGit{revParse: []byte("main\n"), status: []byte("")},
			want: State{Branch: "main"},
		},
		{
			name: "dirty repository reports the branch and the bit",
			git:  scriptedGit{revParse: []byte("amx/feature\n"), status: []byte(" M internal/gitstate/cache.go\n")},
			want: State{Branch: "amx/feature", Dirty: true},
		},
		{
			name: "detached HEAD yields an empty branch and still reports dirt",
			git:  scriptedGit{revParse: []byte("HEAD\n"), status: []byte(" M main.go\n")},
			want: State{Dirty: true},
		},
		{
			name: "non-repository directory yields the zero value",
			git:  scriptedGit{revParseErr: notARepo, statusErr: notARepo},
			want: State{},
		},
		{
			name: "missing git binary yields the zero value",
			git:  scriptedGit{err: missingBinary},
			want: State{},
		},
		{
			name: "timeout yields the zero value",
			git:  scriptedGit{err: timeout},
			want: State{},
		},
		{
			name: "whitespace-only status is not dirty",
			git:  scriptedGit{revParse: []byte("main\n"), status: []byte("\n")},
			want: State{Branch: "main"},
		},
		{
			name: "multi-line rev-parse output is treated as no branch",
			git:  scriptedGit{revParse: []byte("main\nother\n"), status: []byte("")},
			want: State{},
		},
		{
			name: "a failed branch probe does not suppress the dirty bit",
			git:  scriptedGit{revParseErr: notARepo, status: []byte(" M x\n")},
			want: State{Dirty: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			git := test.git
			got := Probe(context.Background(), git.run, "/tmp/checkout", ProbeTimeout)
			if got != test.want {
				t.Fatalf("Probe() = %+v, want %+v", got, test.want)
			}
			for _, call := range git.calls {
				if call.directory != "/tmp/checkout" {
					t.Fatalf("probe ran in %q, want /tmp/checkout", call.directory)
				}
				if call.timeout != ProbeTimeout {
					t.Fatalf("probe timeout = %s, want %s", call.timeout, ProbeTimeout)
				}
			}
		})
	}
}

// The two commands are fixed by the package contract; session 7 renders what
// they produce, so a silent change of either would change the meaning of the
// column without changing its type.
func TestProbeRunsTheDocumentedCommands(t *testing.T) {
	t.Parallel()

	git := &scriptedGit{revParse: []byte("main\n"), status: []byte("")}
	Probe(context.Background(), git.run, "/tmp/checkout", ProbeTimeout)
	if len(git.calls) != 2 {
		t.Fatalf("probe made %d git calls, want 2", len(git.calls))
	}
	if got := strings.Join(git.calls[0].args, " "); got != "rev-parse --abbrev-ref HEAD" {
		t.Fatalf("branch probe ran %q", got)
	}
	if got := strings.Join(git.calls[1].args, " "); got != "status --porcelain --untracked-files=no --ignore-submodules=untracked" {
		t.Fatalf("dirty probe ran %q", got)
	}
}

func TestProbeWithoutAnExecutorIsSilent(t *testing.T) {
	t.Parallel()

	if got := Probe(context.Background(), nil, "/tmp/checkout", ProbeTimeout); got != (State{}) {
		t.Fatalf("Probe(nil executor) = %+v, want the zero value", got)
	}
}

func TestProbeShowsTrackedSubmoduleChangesDespiteGitDisplaySettings(t *testing.T) {
	t.Parallel()
	for _, setting := range []string{"diff.ignoreSubmodules", "submodule.child.ignore"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			dir, git, write := reviewFixture(t)
			child, _, _ := reviewFixture(t)
			git("-c", "protocol.file.allow=always", "submodule", "add", child, "child")
			git("-c", "commit.gpgsign=false", "commit", "-qam", "add submodule")
			git("config", setting, "all")

			// The dashboard's dirty bit excludes untracked files, including
			// those inside a submodule, even while overriding display settings.
			write("child/untracked", "new file\n")
			if got := Probe(context.Background(), runGit, dir, ProbeTimeout); got.Dirty {
				t.Fatalf("untracked submodule file marked the checkout dirty: %+v", got)
			}
			write("child/tracked", "unfinished work\n")
			if got := Probe(context.Background(), runGit, dir, ProbeTimeout); !got.Dirty {
				t.Fatalf("probe hid tracked submodule changes with %s=all: %+v", setting, got)
			}
		})
	}
}

func TestBoundedBufferKeepsOnlyTheHead(t *testing.T) {
	t.Parallel()

	buf := &boundedBuffer{limit: 4}
	n, err := buf.Write([]byte("abcdefgh"))
	if err != nil || n != 8 {
		t.Fatalf("Write() = %d, %v; want 8, nil — the writer must never make git block", n, err)
	}
	if got := string(buf.Bytes()); got != "abcd" {
		t.Fatalf("retained %q, want %q", got, "abcd")
	}
	if _, err := buf.Write([]byte("ijkl")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	if got := string(buf.Bytes()); got != "abcd" {
		t.Fatalf("retained %q after overflow, want %q", got, "abcd")
	}
}
