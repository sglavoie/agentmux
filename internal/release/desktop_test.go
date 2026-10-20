package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The desktop gate runs here against the same stand-in world as the paired
// gate, plus a fake macos/scripts/release-gate.sh committed into the Agent Mux
// checkout. Git and ssh-keygen are real; Xcode, the app, and the other Mac are
// what the fakes replace.

const fakeToolchainFacts = `route: remote
remote_host: tester@other-mac.local
build_host: Other Mac
build_os_version: 27.0.1
build_arch: arm64
xcode: Xcode 27.0 Build version 27A266a
developer_id_identities: 0`

const fakeBundleFacts = `bundle_version: 0.1.0
amx_version: amx 0.8.0 (revision 0123456789ab)
amx_sha256: aaaa
tmux_version: tmux 3.7c
signature: ad-hoc
distribution: this-mac-only`

func newDesktopFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	script := filepath.Join(f.agentMuxRoot, "macos", "scripts", "release-gate.sh")
	mkdir(t, filepath.Dir(script))
	writeScript(t, script, `#!/bin/sh
if [ -n "${FAKE_GATE_LOG:-}" ]; then
    echo "$*" >> "$FAKE_GATE_LOG"
fi
case "$1" in
toolchain)
    if [ -n "${FAKE_TOOLCHAIN_MISSING:-}" ]; then
        echo "route: refuse"
        echo "pending: $FAKE_TOOLCHAIN_MISSING" >&2
        exit 75
    fi
    cat <<'FACTS'
`+fakeToolchainFacts+`
FACTS
    ;;
bundle)
    echo "valid on disk"
    cat <<'FACTS'
`+fakeBundleFacts+`
FACTS
    ;;
esac
`)
	run(t, f.agentMuxRoot, "git", "add", "-A")
	run(t, f.agentMuxRoot, "git", "commit", "-m", "desktop gate script")
	return f
}

func (f *fixture) desktopOptions() DesktopOptions {
	return DesktopOptions{
		AgentMuxRoot: f.agentMuxRoot,
		EvidenceDir:  f.evidenceDir,
		Run:          f.exec,
		Now:          steppingClock(),
	}
}

func desktopStep(t *testing.T, ev *DesktopEvidence, name string) StepResult {
	t.Helper()
	for _, step := range ev.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("no step named %q in %+v", name, ev.Steps)
	return StepResult{}
}

func fact(facts []Fact, name string) string {
	for _, f := range facts {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func TestDesktopGatePassesAndRecordsWhatItBuilt(t *testing.T) {
	t.Parallel()

	f := newDesktopFixture(t)
	justLog := filepath.Join(t.TempDir(), "just.log")
	f.setenv("FAKE_JUST_LOG", justLog)

	ev, err := RunDesktop(context.Background(), f.desktopOptions())
	if err != nil {
		t.Fatalf("RunDesktop: %v", err)
	}
	if !ev.Passed() || ev.Result != StatusPassed {
		t.Fatalf("result = %q, want passed", ev.Result)
	}
	if ev.Rehearsal || ev.TreeSHA256 != "" {
		t.Errorf("a clean tree is not a rehearsal: rehearsal=%v tree=%q", ev.Rehearsal, ev.TreeSHA256)
	}
	if ev.Gate != DesktopGateName || ev.SchemaVersion != DesktopSchemaVersion {
		t.Errorf("gate = %q schema = %d", ev.Gate, ev.SchemaVersion)
	}
	revision := revisionOf(t, f.agentMuxRoot)
	if ev.Repository.Revision != revision || ev.RecordID != DesktopRecordID(revision, "") {
		t.Errorf("record %q for revision %q, want the ID of %q", ev.RecordID, ev.Repository.Revision, revision)
	}
	for _, step := range ev.Steps {
		if step.Status != StatusPassed {
			t.Errorf("step %s is %s", step.Name, step.Status)
		}
	}
	if got := ev.Environment.OSVersion; got != fakeOSVersion {
		t.Errorf("os version = %q", got)
	}
	if got := fact(ev.Environment.Toolchain, "xcode"); got != "Xcode 27.0 Build version 27A266a" {
		t.Errorf("toolchain xcode = %q", got)
	}
	if got := fact(ev.Environment.Toolchain, "build_host"); got != "Other Mac" {
		t.Errorf("toolchain build_host = %q", got)
	}
	if got := fact(ev.Bundle, "signature"); got != "ad-hoc" {
		t.Errorf("bundle signature = %q", got)
	}
	// Output that is not a fact does not become one.
	for _, f := range ev.Bundle {
		if strings.Contains(f.Name, "valid") {
			t.Errorf("non-fact line recorded as a fact: %+v", f)
		}
	}
	if got, want := strings.Fields(readFile(t, justLog)), []string{"mac-app", "mac-test", "mac-test"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("just recipes run = %v, want %v", got, want)
	}

	loaded, err := LoadDesktopEvidence(ev.Path)
	if err != nil {
		t.Fatalf("LoadDesktopEvidence: %v", err)
	}
	if loaded.RecordID != ev.RecordID || !loaded.Passed() {
		t.Errorf("record on disk = %+v", loaded)
	}
	if loaded.Signature.Signed {
		t.Error("a run with no key wrote a record that says it is signed")
	}
	// A pair's reader must not take a desktop record for a pair's.
	if _, err := LoadEvidence(ev.Path); err == nil {
		t.Error("the paired gate's reader accepts a desktop record")
	}

	// The same revision addresses the same record, and a second run does not
	// silently replace the first.
	if _, err := RunDesktop(context.Background(), f.desktopOptions()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second run over the same revision: err = %v, want a refusal to overwrite", err)
	}
}

func TestDesktopGateReportsMissingInfrastructureAsPending(t *testing.T) {
	t.Parallel()

	f := newDesktopFixture(t)
	justLog := filepath.Join(t.TempDir(), "just.log")
	f.setenv("FAKE_JUST_LOG", justLog)
	f.setenv("FAKE_TOOLCHAIN_MISSING", "no Xcode on the Mac that builds")

	ev, err := RunDesktop(context.Background(), f.desktopOptions())
	if !errors.Is(err, ErrDesktopGatePending) {
		t.Fatalf("err = %v, want ErrDesktopGatePending", err)
	}
	if errors.Is(err, ErrDesktopGateFailed) {
		t.Error("pending is reported as failed too; the two call for different responses")
	}
	if ev.Passed() || ev.Result != StatusPending {
		t.Fatalf("result = %q, want pending and not passed", ev.Result)
	}
	if ev.PendingReason != "no Xcode on the Mac that builds" {
		t.Errorf("pending reason = %q", ev.PendingReason)
	}
	if got := desktopStep(t, ev, StepDesktopToolchain).Status; got != StatusPending {
		t.Errorf("toolchain step = %q, want pending", got)
	}
	for _, name := range []string{"build-app", StepDesktopBundle, "hosted-tests-debug", "hosted-tests-release"} {
		if got := desktopStep(t, ev, name).Status; got != StatusNotRun {
			t.Errorf("step %s = %q after a pending toolchain, want not-run", name, got)
		}
	}
	if _, statErr := os.Stat(justLog); statErr == nil {
		t.Errorf("recipes ran with no toolchain: %s", readFile(t, justLog))
	}
	loaded, err := LoadDesktopEvidence(ev.Path)
	if err != nil {
		t.Fatalf("LoadDesktopEvidence: %v", err)
	}
	if loaded.Passed() || loaded.Result != StatusPending || len(loaded.Bundle) != 0 {
		t.Errorf("pending record on disk = %+v", loaded)
	}
}

func TestDesktopGateRefusesADirtyTreeUnlessRehearsing(t *testing.T) {
	t.Parallel()

	f := newDesktopFixture(t)
	gateLog := filepath.Join(t.TempDir(), "gate.log")
	f.setenv("FAKE_GATE_LOG", gateLog)
	writeFile(t, filepath.Join(f.agentMuxRoot, "untracked.go"), "package main\n")

	ev, err := RunDesktop(context.Background(), f.desktopOptions())
	if !errors.Is(err, ErrDesktopGateFailed) {
		t.Fatalf("err = %v, want ErrDesktopGateFailed", err)
	}
	if ev.Passed() || ev.Rehearsal || ev.Repository.Clean {
		t.Errorf("dirty tree: result=%q rehearsal=%v clean=%v", ev.Result, ev.Rehearsal, ev.Repository.Clean)
	}
	if _, statErr := os.Stat(gateLog); statErr == nil {
		t.Errorf("steps ran over a dirty tree that was not a rehearsal: %s", readFile(t, gateLog))
	}
	if entries, _ := os.ReadDir(f.evidenceDir); len(entries) != 0 {
		t.Errorf("a refused run left %d file(s) in the evidence directory", len(entries))
	}

	opts := f.desktopOptions()
	opts.Rehearsal = true
	first, err := RunDesktop(context.Background(), opts)
	if err != nil {
		t.Fatalf("rehearsal: %v", err)
	}
	if !first.Passed() || !first.Rehearsal || first.TreeSHA256 == "" {
		t.Fatalf("rehearsal: result=%q rehearsal=%v tree=%q", first.Result, first.Rehearsal, first.TreeSHA256)
	}
	if first.RecordID == DesktopRecordID(first.Repository.Revision, "") {
		t.Error("a rehearsal over a dirty tree has the record ID of the clean revision")
	}

	// Another tree on the same revision is another record.
	writeFile(t, filepath.Join(f.agentMuxRoot, "untracked.go"), "package main\n\nvar changed = true\n")
	opts.Now = steppingClock()
	second, err := RunDesktop(context.Background(), opts)
	if err != nil {
		t.Fatalf("second rehearsal: %v", err)
	}
	if second.TreeSHA256 == first.TreeSHA256 || second.RecordID == first.RecordID {
		t.Error("two different working trees produced the same tree digest or record ID")
	}
}

func TestDesktopGateStopsAtTheFirstFailedStep(t *testing.T) {
	t.Parallel()

	f := newDesktopFixture(t)
	f.setenv("FAKE_JUST_FAIL", "mac-app")

	ev, err := RunDesktop(context.Background(), f.desktopOptions())
	if !errors.Is(err, ErrDesktopGateFailed) {
		t.Fatalf("err = %v, want ErrDesktopGateFailed", err)
	}
	build := desktopStep(t, ev, "build-app")
	if build.Status != StatusFailed || !strings.Contains(build.OutputTail, "failed on purpose") {
		t.Errorf("build-app = %+v", build)
	}
	for _, name := range []string{StepDesktopBundle, "hosted-tests-debug", "hosted-tests-release"} {
		if got := desktopStep(t, ev, name).Status; got != StatusNotRun {
			t.Errorf("step %s = %q after a failed build, want not-run", name, got)
		}
	}
	if ev.Passed() || len(ev.Bundle) != 0 {
		t.Errorf("failed run: result=%q bundle=%v", ev.Result, ev.Bundle)
	}
}

func TestDesktopGateSignsUnderItsOwnNamespace(t *testing.T) {
	t.Parallel()

	f := newDesktopFixture(t)
	opts := f.desktopOptions()
	opts.SigningKey = f.signingKey

	ev, err := RunDesktop(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunDesktop: %v", err)
	}
	if !ev.Signature.Signed || ev.Signature.Namespace != DesktopSigningNamespace {
		t.Fatalf("signature = %+v", ev.Signature)
	}
	verify := func(namespace string) []string {
		return []string{"ssh-keygen", "-Y", "verify", "-f", f.publicKey, "-I", "release@test", "-n", namespace, "-s", SignaturePath(ev.Path)}
	}
	verifySignature(t, verify(DesktopSigningNamespace), ev.Path)
	// A desktop signature is not a pair's release approval.
	requireNoValidSignature(t, verify(SigningNamespace), ev.Path)
}

func TestDesktopPlanAsksForNotarizationOnlyWhenTold(t *testing.T) {
	t.Parallel()

	joined := func(steps []Step) string {
		var b strings.Builder
		for _, step := range steps {
			b.WriteString(strings.Join(step.Argv, " ") + "\n")
		}
		return b.String()
	}
	if strings.Contains(joined(DesktopPlan(false)), "--notarized") {
		t.Error("the default plan requires notarization")
	}
	if got := strings.Count(joined(DesktopPlan(true)), "--notarized"); got != 2 {
		t.Errorf("--notarized reaches %d steps, want the toolchain and the bundle", got)
	}
}

// TestTheTwoGatesStayApart holds the rule the desktop gate exists to keep: the
// paired gate never needs Xcode, and the desktop gate never does what only a
// paired release may do to a machine.
func TestTheTwoGatesStayApart(t *testing.T) {
	t.Parallel()

	for _, step := range Plan("/amx", "/asl") {
		argv := strings.Join(step.Argv, " ")
		for _, forbidden := range []string{"mac-", "macos/", "xcodebuild", "desktop"} {
			if strings.Contains(argv, forbidden) {
				t.Errorf("paired gate step %q reaches %q: %s", step.Name, forbidden, argv)
			}
		}
	}
	for _, step := range DesktopPlan(true) {
		argv := strings.Join(step.Argv, " ")
		for _, forbidden := range []string{"install", "restart", "tag", "paired-release-gate", "asl", "uninstall"} {
			if strings.Contains(argv, forbidden) {
				t.Errorf("desktop gate step %q reaches %q: %s", step.Name, forbidden, argv)
			}
		}
		if step.Repo != AgentMuxRepo {
			t.Errorf("desktop gate step %q runs in %q", step.Name, step.Repo)
		}
	}
}
