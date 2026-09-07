package overseer

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/overseerpolicy"
	"github.com/spf13/cobra"
)

// policyData is the versioned overseer policy payload carried inside the shared
// schema-v1 data envelope.
//
// It answers one question completely: what was decided, for which repositories,
// under which configuration, and which applicable declarations were examined to
// get there. There is deliberately no expiry, no token, and no grant identifier
// — nothing here is meant to be stored and presented later, which is why the
// only two opaque strings in it name the configuration and the request rather
// than conferring anything.
type policyData struct {
	Contract contractView `json:"contract"`
	// Plan is the caller's own plan identifier, echoed back so an answer read
	// out of a log still says what it was about.
	Plan   string `json:"plan,omitempty"`
	Access string `json:"access"`
	// Allowed is the plan-level answer: true only when every repository was
	// permitted.
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	Summary string `json:"summary"`
	// ConfigFingerprint identifies the project declarations this decision was
	// made against. A caller holding an older answer can compare it to detect
	// that configuration has moved underneath it.
	ConfigFingerprint string `json:"config_fingerprint"`
	// RequestDigest identifies the question. It changes with the access
	// category, the plan, the repository set, and the fingerprint above.
	RequestDigest string           `json:"request_digest"`
	Repositories  []repositoryView `json:"repositories"`
}

// contractView is the separately versioned overseer capability contract. It
// travels inside the shared envelope but versions independently of it, and it
// enumerates the access categories this version understands so a caller can
// discover the vocabulary from any answer it already has.
type contractView struct {
	Name             string   `json:"name"`
	Version          int      `json:"version"`
	AccessCategories []string `json:"access_categories"`
}

// repositoryView is one repository's outcome. Every repository is reported,
// permitted or not, so an operator sees the whole picture rather than the first
// thing that went wrong.
type repositoryView struct {
	Alias string `json:"alias,omitempty"`
	// Path is the requested path, exactly as the caller wrote it.
	Path string `json:"path"`
	// ResolvedPath is the canonical, symlink-evaluated directory, or empty when
	// resolution itself failed.
	ResolvedPath string `json:"resolved_path,omitempty"`
	Allowed      bool   `json:"allowed"`
	Reason       string `json:"reason"`
	Detail       string `json:"detail,omitempty"`
	// Declarations is every configured project that applies to this directory,
	// in configuration order — the full set that was examined, not only the one
	// that decided.
	Declarations []declarationView `json:"declarations"`
}

// declarationView is one applicable project declaration. Configured and OptIn
// are reported separately because "asked and declined" and "never asked"
// exclude alike but are not the same operator decision, and the line to edit
// differs.
type declarationView struct {
	Title   string `json:"title,omitempty"`
	Profile string `json:"profile,omitempty"`
	// Directory is the canonicalized directory the declaration governs.
	Directory string `json:"directory"`
	// ConfiguredDirectory is the directory as written in the config file, which
	// is what an operator has to search for to change the answer.
	ConfiguredDirectory string `json:"configured_directory"`
	Configured          bool   `json:"configured"`
	OptIn               bool   `json:"opt_in"`
}

func repositoryViews(decisions []overseerpolicy.RepositoryDecision) []repositoryView {
	views := make([]repositoryView, 0, len(decisions))
	for _, decision := range decisions {
		views = append(views, repositoryView{
			Alias:        decision.Alias,
			Path:         decision.Path,
			ResolvedPath: decision.ResolvedPath,
			Allowed:      decision.Allowed,
			Reason:       string(decision.Reason),
			Detail:       decision.Detail,
			Declarations: declarationViews(decision.Declarations),
		})
	}
	return views
}

func declarationViews(declarations []overseerpolicy.Declaration) []declarationView {
	views := make([]declarationView, 0, len(declarations))
	for _, declaration := range declarations {
		views = append(views, declarationView{
			Title:               declaration.Title,
			Profile:             declaration.Profile,
			Directory:           declaration.Directory,
			ConfiguredDirectory: declaration.ConfiguredDirectory,
			Configured:          declaration.Configured,
			OptIn:               declaration.OptIn,
		})
	}
	return views
}

// writePolicyText renders the same decision for a human. It carries every field
// the JSON payload does, so a text-mode operator diagnosing a refusal is not
// told less than a script would be.
func writePolicyText(cmd *cobra.Command, data policyData) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "contract: %s/%d\n", data.Contract.Name, data.Contract.Version)
	if data.Plan != "" {
		_, _ = fmt.Fprintf(out, "plan: %s\n", data.Plan)
	}
	_, _ = fmt.Fprintf(out, "access: %s\n", data.Access)
	_, _ = fmt.Fprintf(out, "decision: %s\n", decisionWord(data.Allowed))
	_, _ = fmt.Fprintf(out, "reason: %s\n", data.Reason)
	_, _ = fmt.Fprintf(out, "config_fingerprint: %s\n", data.ConfigFingerprint)
	_, _ = fmt.Fprintf(out, "request_digest: %s\n", data.RequestDigest)
	_, _ = fmt.Fprintf(out, "summary: %s\n", data.Summary)
	for _, repository := range data.Repositories {
		_, _ = fmt.Fprintf(out, "repository\t%s\t%s\t%s\t%s\t%s\n",
			repositoryReference(repository), decisionWord(repository.Allowed), repository.Reason, repository.ResolvedPath, repository.Detail)
		for _, declaration := range repository.Declarations {
			_, _ = fmt.Fprintf(out, "  declaration\t%s\t%s\t%s\n",
				declaration.Title, declaration.ConfiguredDirectory, declarationAnswer(declaration))
		}
	}
}

func repositoryReference(repository repositoryView) string {
	if repository.Alias != "" {
		return repository.Alias
	}
	return repository.Path
}

func decisionWord(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "refused"
}

// declarationAnswer names the operator's answer using the same three states the
// config file keeps apart, so a text reader can tell a decline from a project
// that was never asked.
func declarationAnswer(declaration declarationView) string {
	switch {
	case declaration.OptIn:
		return "opted_in"
	case declaration.Configured:
		return "declined"
	default:
		return "unenrolled"
	}
}
