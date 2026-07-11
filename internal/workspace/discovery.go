package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

const (
	CandidateSourceLive       = "live"
	CandidateSourceTranscript = "transcript"
	CandidateSourcePlanPath   = "plan_path"
	CandidateSourcePlanTitle  = "plan_title"
	ConfidenceHigh            = "high"
	ConfidenceLow             = "low"
)

// PlanEvidence is the discovery subset of one indexed plan. Content should
// contain its Markdown source rather than only the display snippet.
type PlanEvidence struct {
	ID      string
	Name    string
	Content string
}

type DiscoveryOptions struct {
	Config        *config.Config
	LiveSessions  []session.RunningSession
	Conversations []transcript.SessionMeta
	Plans         []PlanEvidence
	Now           time.Time
}

type CandidateObservation struct {
	SourceType   string `json:"source_type"`
	SourceID     string `json:"source_id"`
	ObservedPath string `json:"observed_path,omitempty"`
	Hint         string `json:"hint,omitempty"`
	Confidence   string `json:"confidence"`
}

// ProjectCandidate is a durable, unconfigured canonical directory identity.
type ProjectCandidate struct {
	ID            string                 `json:"id"`
	CanonicalPath string                 `json:"canonical_path"`
	GitCommonDir  string                 `json:"git_common_dir,omitempty"`
	DisplayName   string                 `json:"display_name"`
	FirstSeenAt   time.Time              `json:"first_seen_at"`
	LastSeenAt    time.Time              `json:"last_seen_at"`
	Observations  []CandidateObservation `json:"observations"`
}

type discoveredCandidate struct {
	ProjectCandidate
	identityKey string
}

// DiscoverCandidates merges strong path evidence into durable candidates.
// Title-only plan matches can annotate an existing candidate but never create
// one on their own.
func (s *Service) DiscoverCandidates(ctx context.Context, opts DiscoveryOptions) ([]ProjectCandidate, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("workspace: nil datastore")
	}
	if opts.Config == nil {
		return nil, errors.New("workspace: discovery requires configuration")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	configured, err := buildDefinitions(ctx, configuredProjects(opts.Config))
	if err != nil {
		return nil, err
	}
	configuredKeys := make(map[string]struct{}, len(configured))
	for _, def := range configured {
		configuredKeys[def.key] = struct{}{}
	}
	configuredResolver := projectresolve.NewWithGit(configuredProjects(opts.Config))

	byIdentity := map[string]*discoveredCandidate{}
	addPath := func(path, sourceType, sourceID string) error {
		if len(configuredResolver.Matches(path)) > 0 {
			return nil
		}
		candidate, ok, err := candidateFromPath(ctx, path)
		if err != nil || !ok {
			return err
		}
		if _, exists := configuredKeys[candidate.identityKey]; exists {
			return nil
		}
		item := byIdentity[candidate.identityKey]
		if item == nil {
			copy := candidate
			item = &copy
			byIdentity[candidate.identityKey] = item
		}
		item.Observations = appendUniqueObservation(item.Observations, CandidateObservation{
			SourceType: sourceType, SourceID: sourceID, ObservedPath: projectresolve.AbsPath(path), Confidence: ConfidenceHigh,
		})
		return nil
	}
	for _, live := range opts.LiveSessions {
		if err := addPath(live.Directory, CandidateSourceLive, fmt.Sprintf("window:%d", live.WindowID)); err != nil {
			return nil, err
		}
	}
	for _, conversation := range opts.Conversations {
		if conversation.HiddenFromIndex {
			continue
		}
		if err := addPath(conversation.CWD, CandidateSourceTranscript, transcript.IndexKey(conversation.Harness, conversation.ID)); err != nil {
			return nil, err
		}
	}
	for _, plan := range opts.Plans {
		for _, path := range absoluteDirectoryPaths(plan.Content) {
			if err := addPath(path, CandidateSourcePlanPath, plan.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, plan := range opts.Plans {
		for _, candidate := range byIdentity {
			if titleHintMatches(plan.Name, candidate.DisplayName) {
				candidate.Observations = appendUniqueObservation(candidate.Observations, CandidateObservation{
					SourceType: CandidateSourcePlanTitle, SourceID: plan.ID, Hint: plan.Name, Confidence: ConfidenceLow,
				})
			}
		}
	}

	if err := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		for _, candidate := range byIdentity {
			if err := persistCandidate(ctx, tx, *candidate, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.ListCandidates(ctx)
}

func (s *Service) ListCandidates(ctx context.Context) ([]ProjectCandidate, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
		SELECT id, canonical_path, COALESCE(git_common_dir, ''), display_name,
		       first_seen_at_ns, last_seen_at_ns
		FROM workspace_candidates WHERE workspace_id IS NULL
		ORDER BY last_seen_at_ns DESC, canonical_path, id`)
	if err != nil {
		return nil, fmt.Errorf("workspace: list candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProjectCandidate
	for rows.Next() {
		var item ProjectCandidate
		var first, last int64
		if err := rows.Scan(&item.ID, &item.CanonicalPath, &item.GitCommonDir, &item.DisplayName, &first, &last); err != nil {
			return nil, fmt.Errorf("workspace: scan candidate: %w", err)
		}
		item.FirstSeenAt, item.LastSeenAt = time.Unix(0, first).UTC(), time.Unix(0, last).UTC()
		item.Observations, err = loadCandidateObservations(ctx, s.store.DB(), item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) ResolveCandidate(ctx context.Context, selector string) (ProjectCandidate, error) {
	items, err := s.ListCandidates(ctx)
	if err != nil {
		return ProjectCandidate{}, err
	}
	selector = strings.TrimSpace(selector)
	var matches []ProjectCandidate
	for _, item := range items {
		if strings.EqualFold(selector, item.ID) || canonicalSelector(selector) == item.CanonicalPath {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return ProjectCandidate{}, fmt.Errorf("%w: candidate %q", ErrNotFound, selector)
	}
	return ProjectCandidate{}, fmt.Errorf("workspace: candidate selector %q is ambiguous", selector)
}

func configuredProjects(cfg *config.Config) []config.Session {
	if len(cfg.Projects) > 0 {
		return cfg.Projects
	}
	return cfg.Sessions
}

func candidateFromPath(ctx context.Context, path string) (discoveredCandidate, bool, error) {
	physical := projectresolve.AbsPath(path)
	if physical == "" {
		return discoveredCandidate{}, false, nil
	}
	info, err := os.Stat(physical)
	if err != nil || !info.IsDir() {
		return discoveredCandidate{}, false, nil
	}
	repo, found, err := discoverGitRepository(ctx, physical)
	if err != nil {
		return discoveredCandidate{}, false, err
	}
	identity, canonical, common := "path:"+physical, physical, ""
	if found {
		identity, canonical, common = "git:"+repo.commonDir, repo.root, repo.commonDir
	}
	hash := sha256.Sum256([]byte(identity))
	return discoveredCandidate{ProjectCandidate: ProjectCandidate{
		ID: "candidate-" + hex.EncodeToString(hash[:8]), CanonicalPath: canonical,
		GitCommonDir: common, DisplayName: filepath.Base(canonical),
	}, identityKey: identity}, true, nil
}

func persistCandidate(ctx context.Context, tx *sql.Tx, item discoveredCandidate, now time.Time) error {
	ns := now.UnixNano()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_candidates (
			id, identity_key, canonical_path, git_common_dir, display_name,
			first_seen_at_ns, last_seen_at_ns
		) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?)
		ON CONFLICT(identity_key) DO UPDATE SET
			canonical_path = excluded.canonical_path,
			git_common_dir = excluded.git_common_dir,
			display_name = excluded.display_name,
			last_seen_at_ns = excluded.last_seen_at_ns
	`, item.ID, item.identityKey, item.CanonicalPath, item.GitCommonDir, item.DisplayName, ns, ns)
	if err != nil {
		return fmt.Errorf("workspace: persist candidate: %w", err)
	}
	for _, observation := range item.Observations {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_candidate_observations (
				candidate_id, source_type, source_id, observed_path, hint, confidence,
				first_seen_at_ns, last_seen_at_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(candidate_id, source_type, source_id, observed_path, hint)
			DO UPDATE SET confidence = excluded.confidence, last_seen_at_ns = excluded.last_seen_at_ns
		`, item.ID, observation.SourceType, observation.SourceID, observation.ObservedPath,
			observation.Hint, observation.Confidence, ns, ns)
		if err != nil {
			return fmt.Errorf("workspace: persist candidate observation: %w", err)
		}
	}
	return nil
}

func loadCandidateObservations(ctx context.Context, db *sql.DB, id string) ([]CandidateObservation, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT source_type, source_id, observed_path, hint, confidence
		FROM workspace_candidate_observations WHERE candidate_id = ?
		ORDER BY source_type, source_id, observed_path, hint`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CandidateObservation
	for rows.Next() {
		var item CandidateObservation
		if err := rows.Scan(&item.SourceType, &item.SourceID, &item.ObservedPath, &item.Hint, &item.Confidence); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func appendUniqueObservation(items []CandidateObservation, item CandidateObservation) []CandidateObservation {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func absoluteDirectoryPaths(content string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, field := range strings.Fields(content) {
		path := strings.Trim(field, "`'\"()[]{}<>,;:.")
		if !filepath.IsAbs(path) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		path = projectresolve.AbsPath(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func titleHintMatches(a, b string) bool {
	a, b = normalizedHint(a), normalizedHint(b)
	return len(a) >= 3 && len(b) >= 3 && (strings.Contains(a, b) || strings.Contains(b, a))
}

func normalizedHint(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func canonicalSelector(path string) string {
	if !strings.Contains(path, string(filepath.Separator)) {
		return ""
	}
	return projectresolve.AbsPath(path)
}
