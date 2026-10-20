// Package overseer holds the `amx overseer` command family: the authorization
// surface the ASL overseer asks before it reads project or conversation
// content, prepares a mutation, or dispatches an action.
//
// It is two leaves. `amx overseer policy` decides nothing itself: it reads the
// operator's current configuration, hands the plan's repository set to
// internal/overseerpolicy, and reports what that package answered. `amx overseer
// history` is the one surface in the family that returns conversation content,
// and it asks that same question twice — once for the plan before anything is
// discovered, and once per conversation against the working directory its
// identity metadata names, which must both lie inside an authorized
// repository and pass the policy evaluation on its own — so a transcript is
// admitted before it is opened rather than filtered after it is read. Three
// properties are the whole point of the surface:
//
//   - Configuration is read on every question, not merely on every invocation.
//     There is no cached evaluator and no daemon in the path, and `history`
//     reloads configuration and re-decides the plan's whole repository set,
//     not the conversation's directory alone, before each conversation it
//     opens, because authorization is per plan and a walk of a corpus takes
//     long enough for an opt-in to be withdrawn during it. Withdrawing an
//     opt-in therefore takes effect on the next conversation rather than on
//     the next request or the next restart.
//   - An answer is evidence for one question. The payload carries the
//     configuration fingerprint it was decided against and a digest of the
//     question itself, and it carries no token, expiry, or grant — there is
//     nothing here to hold on to and present later as permission.
//   - A refusal exits nonzero in both output modes. A caller that reads only
//     the process status must never be able to mistake a refusal for consent.
//
// A withdrawal partway through a `history` request stops it: no further
// conversation is opened, and the evidence gathered before it is dropped
// rather than returned beside a refusal, since the client contract is binary
// and a request half-served under a permission that no longer holds is not
// half-authorized. Configuration that becomes unreadable mid-request stops it
// the same way, as an error rather than a decision.
//
// `history` reaches the corpus only through transcript.History and never
// through the transcript index or its search sidecars: a refresh has already
// read every message body into a sidecar, so a cache lookup would be a way to
// serve an excluded conversation without asking about it. Its withheld entries
// name the harness, the session, and the reason and nothing that locates them,
// because the path, the directory, and the project are what an exclusion is
// about.
//
// Each payload is versioned separately from the envelope it travels in. The
// common schema-v1 wrapper is shared with every other JSON command and stays
// where it is; the contract block inside it versions the overseer's own
// capability surface, so this contract can move without moving a version
// number that governs commands with nothing to do with authorization. It is
// likewise independent of the agent-session-loop bridge protocol, which this
// package does not touch. The two payloads also version independently of each
// other: overseer_policy and overseer_history, each with its own digest
// domain.
package overseer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/overseerpolicy"
	"github.com/spf13/cobra"
)

// contractVersion versions the overseer policy payload, independently of the
// schema-v1 envelope it travels in and of the ASL bridge protocol. Bump it when
// the meaning of an existing field changes; purely additive fields do not
// require a bump, exactly as with the shared envelope.
const contractVersion = 1

// contractName lets a caller recognize the contract it is holding without
// inferring it from the command it happened to run.
const contractName = "overseer_policy"

// digestDomain is stamped into every request digest so a digest computed here
// can never collide with one computed over different material elsewhere.
const digestDomain = "overseer/policy-request/1"

// accessCategory is one kind of access an overseer can ask to be authorized
// for. The three come straight from the rule the overseer works under —
// permission is rechecked before reading project or conversation content,
// before preparing a mutation, and before dispatching an action — and they
// exist here to keep those three questions distinguishable.
//
// Every supported category is gated by the same per-project opt-in today, so
// naming one does not currently change the decision. What it changes is what
// the answer is an answer *to*: a decision carrying "context" is not a decision
// about dispatching an action, and the request digest makes that non-negotiable
// rather than a matter of the caller's good manners.
//
// Inspecting minimal identity metadata is deliberately absent. That is the one
// thing allowed before authorization, so a category for it would be a request
// that always succeeds — a permissive default wearing the shape of a check.
type accessCategory struct {
	name        string
	description string
}

var accessCategories = []accessCategory{
	{name: "context", description: "read project or conversation content"},
	{name: "mutation", description: "prepare a mutation to a repository"},
	{name: "action", description: "dispatch an action into a run"},
}

// overseerLoadConfig is the configuration seam. It is called once per
// invocation and never memoized: a decision has to be made against the
// configuration that exists when the question is asked.
var overseerLoadConfig = cli.LoadRunnableConfig

// Command returns the `overseer` command tree so the root command can register
// it. Root assembly is the only consumer.
//
// The tree is built fresh on every call rather than kept as a package-level
// singleton, so flag state cannot survive from one construction to the next.
func Command() *cobra.Command {
	overseerCmd := &cobra.Command{
		Use:   "overseer",
		Short: "Authorization surface for ASL overseer supervision",
		Long: "The overseer command family answers whether the ASL overseer is authorized to act on the repositories of a plan, and returns bounded conversation evidence from the repositories it is authorized for.\n\n" +
			"It reports the operator's current configuration only, and it never writes the asl_overseer opt-in. `policy` reads no project or conversation content at all; `history` reads a conversation only after the plan and that conversation's own working directory have both been authorized.",
	}
	overseerCmd.AddCommand(newPolicyCommand())
	overseerCmd.AddCommand(newHistoryCommand())
	return overseerCmd
}

func newPolicyCommand() *cobra.Command {
	policyCmd := &cobra.Command{
		Use:   "policy",
		Short: "Ask whether the overseer may act on a plan's repositories",
		Long: "Evaluate one authorization question: given the complete set of repositories a plan participates in, and the category of access being requested, may the overseer act?\n\n" +
			"Configuration is read on every invocation. The answer is evidence for the question that was asked — it names the configuration fingerprint it was decided against and digests the request itself — and it is not a lease: ask again before the next access.\n\n" +
			"A refusal exits nonzero in both text and JSON mode, so a caller that branches on the exit status alone fails closed.",
		Args: cobra.NoArgs,
		// A refusal and a structured request error are results, not usage
		// problems: the command has already written the complete answer, so
		// Cobra must not follow it with an error line and a usage dump. Execute
		// owns the stderr line and the process status from here.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          runPolicy,
	}
	flags := policyCmd.Flags()
	flags.Bool("json", false, "emit schema-v1 JSON carrying the versioned overseer_policy payload")
	flags.String("plan", "", "identifier of the plan being authorized, echoed back into the decision")
	flags.String("access", "", "access category being requested: "+strings.Join(accessCategoryNames(), ", "))
	_ = policyCmd.RegisterFlagCompletionFunc("access", cli.FixedCompletions(accessCategoryNames()...))
	flags.StringArray("repository", nil, "repository the plan participates in, as alias=path or path (repeatable); the path must be absolute or start with ~")
	flags.Int("contract-version", contractVersion, "overseer policy contract version the caller understands")
	return policyCmd
}

func runPolicy(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")

	// The request is checked before configuration is read, so an unsupported
	// question is answered the same way whether or not the config file happens
	// to be readable.
	requested, _ := cmd.Flags().GetInt("contract-version")
	if requested != contractVersion {
		return unsupportedRequest(cmd, jsonOutput, "unsupported_contract_version",
			fmt.Sprintf("overseer policy contract version %d is not supported", requested),
			[]cli.Candidate{{ID: strconv.Itoa(contractVersion), DisplayName: fmt.Sprintf("%s contract version %d", contractName, contractVersion)}})
	}

	access, _ := cmd.Flags().GetString("access")
	access = strings.TrimSpace(access)
	if access == "" {
		return policyError(cli.MissingParameter(cmd, jsonOutput, "access", "access category being requested: "+strings.Join(accessCategoryNames(), ", ")))
	}
	if !supportedAccess(access) {
		return unsupportedRequest(cmd, jsonOutput, "unsupported_access",
			fmt.Sprintf("access category %q is not supported by %s contract version %d", access, contractName, contractVersion),
			accessCandidates())
	}

	specs, _ := cmd.Flags().GetStringArray("repository")
	repositories, err := parseRepositories(specs)
	if err != nil {
		return unsupportedRequest(cmd, jsonOutput, "invalid_repository", err.Error(), nil)
	}

	cfg, err := overseerLoadConfig()
	if err != nil {
		return policyError(cli.CommandFailure(cmd, jsonOutput, "config_error", err))
	}

	plan, _ := cmd.Flags().GetString("plan")
	plan = strings.TrimSpace(plan)
	// Built here and discarded when the command returns: the evaluator holds a
	// snapshot of the declarations, and keeping one across invocations is the
	// cached permission this surface exists to avoid.
	evaluator := overseerpolicy.New(cfg.Projects)
	fingerprint := evaluator.Fingerprint()
	decision := evaluator.Evaluate(overseerpolicy.Request{Plan: plan, Repositories: repositories})

	data := policyData{
		Contract:          contractView{Name: contractName, Version: contractVersion, AccessCategories: accessCategoryNames()},
		Plan:              plan,
		Access:            access,
		Allowed:           decision.Allowed,
		Reason:            string(decision.Reason),
		Summary:           decision.Summary(),
		ConfigFingerprint: fingerprint,
		RequestDigest:     requestDigest(access, plan, repositories, fingerprint),
		Repositories:      repositoryViews(decision.Repositories),
	}

	if jsonOutput {
		if err := cli.WriteJSON(cmd, data); err != nil {
			return err
		}
	} else {
		writePolicyText(cmd, data)
	}
	if !data.Allowed {
		// The decision is complete and already written; the nonzero status is
		// there so `amx overseer policy … && act` cannot proceed on a refusal.
		return &cli.ExitError{Code: 1, Err: errors.New(data.Summary), Quiet: true}
	}
	return nil
}

// unsupportedRequest reports a request this contract version cannot answer,
// carrying what it could have answered instead so a JSON caller can correct
// itself without an interactive prompt.
func unsupportedRequest(cmd *cobra.Command, jsonOutput bool, code, message string, candidates []cli.Candidate) error {
	if !jsonOutput {
		return policyError(errors.New(message))
	}
	return cli.WriteJSONError(cmd, cli.JSONError{Code: code, Message: message, Candidates: candidates})
}

// policyError hands a failure back in the form Execute prints. Because the
// command silences Cobra's own error output, a bare error would exit nonzero
// with nothing on stderr; wrapping it in a non-quiet ExitError puts the message
// back. An error that already carries its own exit contract — every structured
// JSON failure does — is passed through untouched.
func policyError(err error) error {
	if err == nil {
		return nil
	}
	var exit *cli.ExitError
	if errors.As(err, &exit) {
		return err
	}
	return &cli.ExitError{Code: 1, Err: err}
}

func accessCategoryNames() []string {
	names := make([]string, 0, len(accessCategories))
	for _, category := range accessCategories {
		names = append(names, category.name)
	}
	return names
}

func accessCandidates() []cli.Candidate {
	candidates := make([]cli.Candidate, 0, len(accessCategories))
	for _, category := range accessCategories {
		candidates = append(candidates, cli.Candidate{ID: category.name, DisplayName: category.description})
	}
	return candidates
}

// supportedAccess matches exactly, without case folding. The category is
// carried into the request digest, so accepting "Context" as "context" would
// let two spellings of one request produce two different digests.
func supportedAccess(name string) bool {
	for _, category := range accessCategories {
		if category.name == name {
			return true
		}
	}
	return false
}

// parseRepositories turns the repeated --repository flag into the request's
// repository set.
//
// A value is read as alias=path when the text before the first `=` is a
// non-empty alias — no path separator and no leading `~` — and as a bare path
// otherwise, so `/srv/a=b` stays one path while `api=/srv/a` is an alias and a
// path. A path with no alias is legal; the refusal then names the path.
func parseRepositories(specs []string) ([]overseerpolicy.Repository, error) {
	repositories := make([]overseerpolicy.Repository, 0, len(specs))
	seen := make(map[string]string, len(specs))
	for _, spec := range specs {
		alias, path := splitRepositorySpec(strings.TrimSpace(spec))
		if path == "" {
			return nil, fmt.Errorf("repository %q names no path", spec)
		}
		// A relative path would resolve against whatever directory the caller
		// happened to be in, which is the one way to get a confident answer
		// about a repository nobody asked about.
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~") {
			return nil, fmt.Errorf("repository path %q must be absolute or start with ~", path)
		}
		if alias != "" {
			if previous, ok := seen[alias]; ok {
				return nil, fmt.Errorf("repository alias %q was given twice, for %q and %q", alias, previous, path)
			}
			seen[alias] = path
		}
		repositories = append(repositories, overseerpolicy.Repository{Alias: alias, Path: path})
	}
	return repositories, nil
}

func splitRepositorySpec(spec string) (alias, path string) {
	index := strings.Index(spec, "=")
	if index <= 0 {
		return "", spec
	}
	candidate := spec[:index]
	if strings.ContainsRune(candidate, filepath.Separator) || strings.ContainsRune(candidate, '/') || strings.HasPrefix(candidate, "~") {
		return "", spec
	}
	return candidate, spec[index+1:]
}

// requestDigest names the question this answer belongs to: the contract that
// governed it, the access category, the plan, the repository set in the order
// it was given, and the configuration fingerprint it was decided against.
//
// It is an identity, not a signature. It carries no secret and proves nothing
// about who produced it; what it does is make "this decision was about that
// request" checkable, so an answer cannot be quietly carried over to a
// different plan, a wider repository set, or a stronger access category.
func requestDigest(access, plan string, repositories []overseerpolicy.Repository, fingerprint string) string {
	sum := sha256.New()
	writeDigestField(sum, digestDomain)
	writeDigestField(sum, contractName)
	writeDigestField(sum, strconv.Itoa(contractVersion))
	writeDigestField(sum, access)
	writeDigestField(sum, plan)
	writeDigestField(sum, fingerprint)
	writeDigestField(sum, strconv.Itoa(len(repositories)))
	for _, repository := range repositories {
		writeDigestField(sum, repository.Alias)
		writeDigestField(sum, repository.Path)
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// writeDigestField length-prefixes a field so no combination of aliases, paths,
// and plan names can encode to the same bytes as a different request.
func writeDigestField(h hash.Hash, field string) {
	_, _ = fmt.Fprintf(h, "%d:%s|", len(field), field)
}
