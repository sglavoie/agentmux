package overseer

// `amx overseer history`: bounded transcript evidence for the conversations a
// plan's authorized repositories participate in.
//
// This is the first leaf in the family that returns conversation content, so it
// is the first one where the difference between "decided not to show it" and
// "did not read it" is observable. The command is built so that the second is
// what happens. It never touches the transcript index or its search sidecars —
// those are populated by a refresh that reads every message body, so a cache hit
// would be a way to serve an excluded conversation's text without ever asking
// about it — and it reaches the corpus only through
// transcript.History, whose signature admits a conversation before it opens one.
//
// Authorization happens twice, and the two questions are different.
//
//   - Once for the plan, before anything is discovered: given the complete set
//     of repositories the plan participates in, may the overseer read context at
//     all? This is the same question `overseer policy --access context` answers,
//     asked by the same evaluator, and a refusal here ends the command with no
//     discovery performed.
//   - Once per conversation, against the working directory its identity metadata
//     names. A conversation is admitted only if that directory lies inside one of
//     the plan's authorized repositories *and* passes the policy evaluation on
//     its own — which examines every declaration applicable to it, so a
//     conversation in a nested directory that an enclosing exclusion covers is
//     refused even though the plan as a whole was permitted.
//
// The second question is asked through an authorizer that holds the evaluator
// built for this invocation and nothing else. There is no memoized answer: the
// same directory asked about twice is decided twice, so a withdrawn opt-in
// stops the next conversation rather than the next process.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/overseerpolicy"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// historyContractVersion versions the history payload independently of the
// policy contract next to it. The two surfaces answer different questions and
// there is no reason a change to one should renumber the other.
const historyContractVersion = 1

// historyContractName lets a caller recognize the contract it is holding.
const historyContractName = "overseer_history"

// historyDigestDomain keeps a history request digest from ever colliding with a
// policy request digest computed over similar-looking material.
const historyDigestDomain = "overseer/history-request/1"

// historyAccess is the access category a history request is. Reading a
// conversation is reading conversation content, which is exactly what the
// `context` category names, and it is fixed rather than a flag: a caller that
// could label this request `action` would be labelling it wrongly, and the label
// is bound into the request digest.
const historyAccess = "context"

// Refusal reasons this command adds on top of the policy vocabulary and the
// transcript package's structural ones.
const (
	// reasonOutOfScope is a conversation working outside every repository the
	// plan participates in. It is checked before the policy question, so a
	// refusal for an unrelated project never reports whether that project
	// happens to be enrolled.
	reasonOutOfScope = "out_of_scope"
	// reasonDirectoryUnresolved is a working directory that cannot be
	// canonicalized — it has been deleted, or it is not a directory. An
	// unresolvable path cannot be shown to be outside an exclusion.
	reasonDirectoryUnresolved = "path_unresolved"
)

// The transcript readers and the config loader are seams for the same reason
// the policy leaf's loader is: a test has to be able to pose a corpus, and
// neither may be memoized across invocations.
var (
	historyReaders = cli.TranscriptReaders
	// historyRun is the corpus seam. It is the metadata-admission entry point in
	// internal/transcript and deliberately not the index: there is no code path
	// from this command to a cached message body.
	historyRun = transcript.History
)

func newHistoryCommand() *cobra.Command {
	historyCmd := &cobra.Command{
		Use:   "history",
		Short: "Return bounded transcript excerpts from a plan's authorized conversations",
		Long: "Return bounded excerpts from harness conversations that worked inside the repositories of an authorized plan.\n\n" +
			"Authorization is asked before any message is read: conversations are discovered from identity metadata alone, each one is decided against the working directory it names, and only an admitted conversation is opened. The transcript metadata cache is not in the path, so a conversation excluded here is not served from a previous refresh.\n\n" +
			"A conversation whose metadata names no working directory, or names more than one, is unavailable: which of its messages belong to which project is a question only its content could answer.\n\n" +
			"Every conversation that was discovered and not returned is reported by harness, session id, and reason — and by nothing that would locate it, since that is what an exclusion withholds.\n\n" +
			"A plan-level refusal exits nonzero in both output modes, before any discovery is performed.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          runHistory,
	}
	flags := historyCmd.Flags()
	flags.Bool("json", false, "Emit the schema-v1 envelope carrying the versioned overseer history payload")
	flags.String("plan", "", "Identifier of the plan being authorized, echoed back into the answer")
	flags.StringArray("repository", nil, "Repository the plan participates in, as alias=path or path (repeatable); the path must be absolute or start with ~")
	flags.Int("contract-version", historyContractVersion, "Overseer history contract version the caller understands")
	flags.Int("limit", 0, fmt.Sprintf("Maximum conversations to return (default %d, maximum %d)", transcript.DefaultHistoryConversations, transcript.MaxHistoryConversations))
	flags.Int("messages", 0, fmt.Sprintf("Maximum messages per conversation, taken from the end (default %d, maximum %d)", transcript.DefaultHistoryMessages, transcript.MaxHistoryMessages))
	flags.Int("max-chars", 0, fmt.Sprintf("Maximum characters per excerpt (default %d, maximum %d)", transcript.DefaultHistoryExcerptChars, transcript.MaxHistoryExcerptChars))
	flags.StringArray("harness", nil, "Restrict the answer to this harness (repeatable); omit for all harnesses")
	flags.String("since", "", "Only consider transcripts modified at or after this RFC 3339 timestamp")
	return historyCmd
}

func runHistory(cmd *cobra.Command, _ []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")

	requested, _ := cmd.Flags().GetInt("contract-version")
	if requested != historyContractVersion {
		return unsupportedRequest(cmd, jsonOutput, "unsupported_contract_version",
			fmt.Sprintf("overseer history contract version %d is not supported", requested),
			[]cli.Candidate{{ID: strconv.Itoa(historyContractVersion), DisplayName: fmt.Sprintf("%s contract version %d", historyContractName, historyContractVersion)}})
	}

	specs, _ := cmd.Flags().GetStringArray("repository")
	repositories, err := parseRepositories(specs)
	if err != nil {
		return unsupportedRequest(cmd, jsonOutput, "invalid_repository", err.Error(), nil)
	}

	sinceText, _ := cmd.Flags().GetString("since")
	since, err := parseSince(sinceText)
	if err != nil {
		return unsupportedRequest(cmd, jsonOutput, "invalid_since", err.Error(), nil)
	}

	limits := transcript.HistoryLimits{}
	limits.Conversations, _ = cmd.Flags().GetInt("limit")
	limits.Messages, _ = cmd.Flags().GetInt("messages")
	limits.ExcerptChars, _ = cmd.Flags().GetInt("max-chars")
	limits = limits.Normalized()

	harnesses, _ := cmd.Flags().GetStringArray("harness")
	harnesses = normalizeHarnesses(harnesses)

	cfg, err := overseerLoadConfig()
	if err != nil {
		return policyError(cli.CommandFailure(cmd, jsonOutput, "config_error", err))
	}

	plan, _ := cmd.Flags().GetString("plan")
	plan = strings.TrimSpace(plan)

	evaluator := overseerpolicy.New(cfg.Projects)
	fingerprint := evaluator.Fingerprint()
	decision := evaluator.Evaluate(overseerpolicy.Request{Plan: plan, Repositories: repositories})

	data := historyData{
		Contract:          contractView{Name: historyContractName, Version: historyContractVersion, AccessCategories: accessCategoryNames()},
		Plan:              plan,
		Access:            historyAccess,
		Allowed:           decision.Allowed,
		Reason:            string(decision.Reason),
		Summary:           decision.Summary(),
		ConfigFingerprint: fingerprint,
		RequestDigest:     historyRequestDigest(plan, repositories, harnesses, since, limits, fingerprint),
		Repositories:      repositoryViews(decision.Repositories),
		Limits:            limitsView{Conversations: limits.Conversations, Messages: limits.Messages, ExcerptChars: limits.ExcerptChars},
		Harnesses:         harnesses,
	}
	if !since.IsZero() {
		data.Since = since.UTC().Format(time.RFC3339)
	}

	if !decision.Allowed {
		// Nothing is discovered on a plan-level refusal. The corpus is not walked
		// even for identity, because the plan that would have justified looking
		// at it was not authorized.
		data.Summary = decision.Summary()
		return writeHistory(cmd, jsonOutput, data)
	}

	authorizer := &planScopedAuthorizer{evaluator: evaluator, scope: authorizedScope(decision)}
	result, err := historyRun(cfg, historyReaders(), authorizer, transcript.HistoryRequest{
		Harnesses: harnesses,
		Since:     since,
		Limits:    limits,
	})
	if err != nil {
		return policyError(cli.CommandFailure(cmd, jsonOutput, "history_error", err))
	}

	data.Examined = result.Examined
	data.Truncated = result.Truncated
	data.Conversations = conversationViews(result.Conversations)
	data.Withheld = withheldViews(result.Withheld)
	data.Warnings = result.Warnings
	data.Summary = historySummary(result)
	return writeHistory(cmd, jsonOutput, data)
}

func writeHistory(cmd *cobra.Command, jsonOutput bool, data historyData) error {
	if jsonOutput {
		if err := cli.WriteJSON(cmd, data); err != nil {
			return err
		}
	} else {
		writeHistoryText(cmd, data)
	}
	if !data.Allowed {
		// The answer is complete and already written; the nonzero status is there
		// so a caller that branches on the exit status alone fails closed.
		return &cli.ExitError{Code: 1, Err: errors.New(data.Summary), Quiet: true}
	}
	return nil
}

// planScopedAuthorizer answers the per-conversation question. It holds the
// evaluator built for this invocation and the resolved directories the plan was
// authorized for, and it decides every conversation from scratch.
type planScopedAuthorizer struct {
	evaluator *overseerpolicy.Evaluator
	// scope is the resolved directory of every repository the plan was
	// authorized for. A conversation outside all of them was not asked about.
	scope []string
}

func (a *planScopedAuthorizer) Authorize(directory string) transcript.Admission {
	resolved, err := resolveDirectory(directory)
	if err != nil {
		return transcript.Admission{Reason: reasonDirectoryUnresolved, Detail: err.Error()}
	}
	// Scope is checked before authorization on purpose. A conversation belonging
	// to some other project is refused as out of scope without the enrollment of
	// that project ever being consulted, so the answer cannot be read as a probe
	// for whether an unrelated directory is opted in.
	if !withinScope(resolved, a.scope) {
		return transcript.Admission{Reason: reasonOutOfScope}
	}
	decision := a.evaluator.Evaluate(overseerpolicy.Request{
		Repositories: []overseerpolicy.Repository{{Path: directory}},
	})
	if !decision.Allowed {
		detail := ""
		if denials := decision.Denials(); len(denials) > 0 {
			detail = denials[0].Detail
		}
		return transcript.Admission{Reason: string(decision.Reason), Detail: detail}
	}
	return transcript.Admission{
		Allowed:  true,
		Reason:   string(overseerpolicy.ReasonAllowed),
		Projects: declaredProjects(decision),
	}
}

// authorizedScope collects the resolved directory of every permitted repository.
// The resolved form is what containment is tested against, so a symlinked plan
// checkout and the real directory a harness recorded compare equal.
func authorizedScope(decision overseerpolicy.Decision) []string {
	scope := make([]string, 0, len(decision.Repositories))
	for _, repository := range decision.Repositories {
		if !repository.Allowed || repository.ResolvedPath == "" {
			continue
		}
		scope = append(scope, repository.ResolvedPath)
	}
	return scope
}

// declaredProjects names the projects the conversation's directory was
// identified as participating in, in configuration order and without
// duplicates. Only an admitted conversation carries them.
func declaredProjects(decision overseerpolicy.Decision) []string {
	var projects []string
	seen := make(map[string]struct{})
	for _, repository := range decision.Repositories {
		for _, declaration := range repository.Declarations {
			title := strings.TrimSpace(declaration.Title)
			if title == "" {
				continue
			}
			if _, duplicate := seen[title]; duplicate {
				continue
			}
			seen[title] = struct{}{}
			projects = append(projects, title)
		}
	}
	return projects
}

// resolveDirectory canonicalizes a working directory the same way the policy
// evaluator canonicalizes a repository path, so the two agree about what one
// directory is.
func resolveDirectory(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return "", errors.New("empty working directory")
	}
	expanded := config.ExpandTilde(directory)
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	evaluated, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		// A transcript can outlive the directory it worked in. That is not an
		// error to report to the operator, but it is also not a directory that
		// can be shown to be outside an exclusion, so it fails closed.
		return "", err
	}
	return filepath.Clean(evaluated), nil
}

// withinScope reports whether a resolved directory is one of the scope
// directories or lies beneath one. Comparison is on path boundaries, so
// `/srv/api-internal` is not inside `/srv/api`.
func withinScope(directory string, scope []string) bool {
	for _, root := range scope {
		if directory == root {
			return true
		}
		if strings.HasPrefix(directory, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func normalizeHarnesses(values []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func parseSince(text string) (time.Time, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since %q is not an RFC 3339 timestamp", text)
	}
	return parsed, nil
}

// historyRequestDigest names the question this answer belongs to. It binds the
// bounds as well as the plan and the repository set, because an answer taken
// under a 10-message bound is not evidence for a request that asked for 200.
func historyRequestDigest(plan string, repositories []overseerpolicy.Repository, harnesses []string, since time.Time, limits transcript.HistoryLimits, fingerprint string) string {
	sum := sha256.New()
	writeDigestField(sum, historyDigestDomain)
	writeDigestField(sum, historyContractName)
	writeDigestField(sum, strconv.Itoa(historyContractVersion))
	writeDigestField(sum, historyAccess)
	writeDigestField(sum, plan)
	writeDigestField(sum, fingerprint)
	writeDigestField(sum, strconv.Itoa(limits.Conversations))
	writeDigestField(sum, strconv.Itoa(limits.Messages))
	writeDigestField(sum, strconv.Itoa(limits.ExcerptChars))
	sinceField := ""
	if !since.IsZero() {
		sinceField = since.UTC().Format(time.RFC3339)
	}
	writeDigestField(sum, sinceField)
	writeDigestField(sum, strconv.Itoa(len(harnesses)))
	for _, harnessName := range harnesses {
		writeDigestField(sum, harnessName)
	}
	writeDigestField(sum, strconv.Itoa(len(repositories)))
	for _, repository := range repositories {
		writeDigestField(sum, repository.Alias)
		writeDigestField(sum, repository.Path)
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}
