// The gate's ordered command plan: what runs, in which repository, in which
// phase, and why that order is the gate's argument rather than an accident.
package release

import "path/filepath"

// Phase names, in execution order.
const (
	PhaseSource      = "source"
	PhaseEnvironment = "environment"
	PhaseBuild       = "build"
	PhaseVerify      = "verify"
	PhaseInstall     = "install"
	PhaseSmoke       = "smoke"
)

// Step names that the gate itself reads output from. The rest are pass/fail
// only; these three feed the evidence record, so they are referenced by name
// rather than by position.
const (
	StepOSVersion     = "os-version"
	StepKittyVersion  = "kitty-version"
	StepHarnessCompat = "harness-compat"
	StepBuildAgentMux = "build-amx"
)

// Plan returns the ordered command list for a gate run over the two named
// checkouts.
//
// The order is the gate's argument, not an implementation detail:
//
//   - environment first, because a missing Kitty or an unsupported harness
//     should cost seconds rather than surfacing after a twenty-minute stress run;
//   - build next, so the artifact being released exists before anything claims
//     to have tested it;
//   - verify next, cheapest gate first (`just check`) and the long stress and
//     crash/restart runs last, so an ordinary regression fails fast;
//   - install after verify, in the documented order -- ASL first, then Agent Mux
//     -- because Agent Mux's own install smoke restarts the daemon, and a daemon
//     restarted against a stale ASL proves nothing about the pair;
//   - smoke last, against the installation the previous phase just produced,
//     with the overseer pair before the cockpit: the overseer smoke needs only
//     the two installed programs, while the cockpit smoke needs a live Kitty
//     window, and a gate should exhaust what it can judge on its own before it
//     depends on the operator's terminal being what it expects.
//
// A step that fails stops the run. Continuing past a failure would produce
// evidence in which later steps ran against a state the record does not
// describe.
func Plan(agentMuxRoot, aslRoot string) []Step {
	aslWrapper := filepath.Join(aslRoot, aslWrapperPath)
	return []Step{
		{Name: StepOSVersion, Repo: AgentMuxRepo, Phase: PhaseEnvironment, Argv: []string{"sw_vers", "-productVersion"}},
		{Name: StepKittyVersion, Repo: AgentMuxRepo, Phase: PhaseEnvironment, Argv: []string{"kitty", "--version"}},
		{Name: StepHarnessCompat, Repo: ASLRepo, Phase: PhaseEnvironment, Argv: []string{aslWrapper, "harness-compat", "--json"}},

		{Name: StepBuildAgentMux, Repo: AgentMuxRepo, Phase: PhaseBuild, Argv: []string{"just", "build"}},

		{Name: "amx-check", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "check"}},
		{Name: "asl-tests", Repo: ASLRepo, Phase: PhaseVerify, Argv: []string{"env", "ASL_CLI_TRACE=0", "just", "test-all"}},
		{Name: "asl-lint", Repo: ASLRepo, Phase: PhaseVerify, Argv: []string{"env", "ASL_CLI_TRACE=0", "just", "lint"}},
		{Name: "contract-sync", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "asl-contract-sync", "asl_root=" + aslRoot}},
		{Name: "paired-integration", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "asl-integration"}},
		{Name: "crash-restart", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "crash-restart"}},
		{Name: "amx-stress", Repo: AgentMuxRepo, Phase: PhaseVerify, Argv: []string{"just", "reliability-stress"}},
		{Name: "asl-stress", Repo: ASLRepo, Phase: PhaseVerify, Argv: []string{"just", "reliability-stress"}},

		{Name: "asl-install", Repo: ASLRepo, Phase: PhaseInstall, Argv: []string{"just", "all"}},
		{Name: "asl-install-verify", Repo: ASLRepo, Phase: PhaseInstall, Argv: []string{"just", "verify-asl-install"}},
		{Name: "amx-install-restart", Repo: AgentMuxRepo, Phase: PhaseInstall, Argv: []string{"just", "install-restart-smoke"}},

		{Name: "overseer-integration", Repo: AgentMuxRepo, Phase: PhaseSmoke, Argv: []string{"just", "overseer-integration", "asl_root=" + aslRoot}},
		{Name: "cockpit-smoke", Repo: AgentMuxRepo, Phase: PhaseSmoke, Argv: []string{"just", "cockpit-smoke"}},
	}
}
