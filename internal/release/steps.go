// Executing planned steps and reading the evidence the gate absorbs from them.

package release

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// absorb reads the output of the steps whose output the record needs. Every
// other step is judged by its exit status alone.
func (g *gate) absorb(step Step, output string, ev *Evidence) error {
	switch step.Name {
	case StepOSVersion:
		ev.Environment.OSVersion = firstLine(output)
	case StepKittyVersion:
		ev.Environment.Kitty = firstLine(output)
	case StepHarnessCompat:
		harnesses, err := parseHarnessCompat(output)
		if err != nil {
			return err
		}
		ev.Environment.Harnesses = harnesses
	case StepBuildAgentMux:
		artifacts, err := g.hashArtifacts()
		if err != nil {
			return err
		}
		ev.Artifacts = artifacts
	}
	return nil
}

// runStep executes one planned step, timing it through the injected clock.
func (g *gate) runStep(ctx context.Context, step Step) (StepResult, string) {
	dir := g.rootFor(step.Repo)
	g.logf("run  %s [%s] %s", step.Name, step.Repo, strings.Join(step.Argv, " "))
	started := g.now()
	output, code, err := g.run(ctx, dir, step.Argv)
	elapsed := g.now().Sub(started)

	result := StepResult{
		Name:       step.Name,
		Repo:       step.Repo,
		Phase:      step.Phase,
		Command:    step.Argv,
		Status:     StatusPassed,
		ExitCode:   code,
		DurationMS: elapsed.Milliseconds(),
	}
	switch {
	case err != nil:
		result.Status = StatusFailed
		result.ExitCode = -1
		result.OutputTail = tail(output + "\n" + err.Error())
	case code != 0:
		result.Status = StatusFailed
		result.OutputTail = tail(output)
	}
	switch {
	case result.Status == StatusPassed:
		g.logf("ok   %s (%s)", step.Name, elapsed.Round(time.Millisecond))
	case g.pendingExit != 0 && result.ExitCode == g.pendingExit:
		// The desktop gate reports this one itself, as pending.
	default:
		g.logf("FAIL %s (exit %d)", step.Name, result.ExitCode)
	}
	return result, output
}

func (g *gate) rootFor(repo string) string {
	if repo == ASLRepo {
		return g.opts.ASLRoot
	}
	return g.opts.AgentMuxRoot
}

// readRepositories records the revision, description, and cleanliness of both
// checkouts. The order is fixed -- Agent Mux, then ASL -- because the pair ID
// folds the two revisions in that order. The desktop gate names the one
// checkout it releases instead.
func (g *gate) readRepositories(ctx context.Context, repos ...RepoEvidence) ([]RepoEvidence, error) {
	if len(repos) == 0 {
		repos = []RepoEvidence{
			{Repo: AgentMuxRepo, Root: g.opts.AgentMuxRoot},
			{Repo: ASLRepo, Root: g.opts.ASLRoot},
		}
	}
	for i := range repos {
		root := repos[i].Root
		rev, err := g.git(ctx, root, "rev-parse", "HEAD")
		if err != nil {
			return repos, fmt.Errorf("release: reading %s revision: %w", repos[i].Repo, err)
		}
		repos[i].Revision = firstLine(rev)

		describe, err := g.git(ctx, root, "describe", "--tags", "--always", "--dirty")
		if err != nil {
			// A repository with no tags at all is normal and must not stop a
			// release; the revision above is the identity that matters.
			describe = repos[i].Revision
		}
		repos[i].Describe = firstLine(describe)

		status, err := g.git(ctx, root, "status", "--porcelain")
		if err != nil {
			return repos, fmt.Errorf("release: reading %s status: %w", repos[i].Repo, err)
		}
		dirty := nonEmptyLines(status)
		repos[i].Clean = len(dirty) == 0
		repos[i].Dirty = dirty
	}
	return repos, nil
}

func (g *gate) git(ctx context.Context, dir string, args ...string) (string, error) {
	argv := append([]string{"git"}, args...)
	output, code, err := g.run(ctx, dir, argv)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("`%s` exited %d: %s", strings.Join(argv, " "), code, strings.TrimSpace(output))
	}
	return output, nil
}

// hashArtifacts records the identity of what is being released: the freshly
// built binary, the ASL wrapper that is about to be stowed, and a digest over
// the ASL package sources.
//
// The source-tree digest exists because ASL ships as Python. There is no
// compiled artifact whose hash pins the code that will run, and hashing only
// the wrapper would pin a twenty-line launcher while the program it launches
// changed underneath it.
func (g *gate) hashArtifacts() ([]Artifact, error) {
	artifacts := []Artifact{
		{Name: "amx", Repo: AgentMuxRepo, Path: "amx", Kind: "file"},
		{Name: "agent-session-loop", Repo: ASLRepo, Path: aslWrapperPath, Kind: "file"},
		{Name: "agent_session_loop", Repo: ASLRepo, Path: "agent_session_loop", Kind: "tree"},
	}
	for i := range artifacts {
		full := filepath.Join(g.rootFor(artifacts[i].Repo), artifacts[i].Path)
		var (
			digest string
			err    error
		)
		if artifacts[i].Kind == "tree" {
			digest, err = sha256Tree(full, ".py")
		} else {
			digest, err = sha256File(full)
		}
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", artifacts[i].Name, err)
		}
		artifacts[i].SHA256 = digest
	}
	return artifacts, nil
}

func (g *gate) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(g.progress, format+"\n", args...)
}
