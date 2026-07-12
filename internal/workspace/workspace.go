// Package workspace builds stable project workspaces from the configured
// launch entries. A workspace is the repository (when Git metadata is
// available) or canonical physical directory that those entries share.
package workspace

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

const (
	gitProbeTimeout           = 2 * time.Second
	workspaceSearchEntityType = "project"
)

var (
	// ErrNotFound reports a selector that does not identify a configured
	// workspace.
	ErrNotFound = errors.New("workspace: not found")
)

// Candidate is the safe, user-facing information returned when a selector is
// ambiguous. Callers should show these candidates and never guess.
type Candidate struct {
	ID            string
	DisplayName   string
	CanonicalPath string
}

// AmbiguousSelectorError reports a selector matching more than one workspace.
type AmbiguousSelectorError struct {
	Selector   string
	Candidates []Candidate
}

func (e *AmbiguousSelectorError) Error() string {
	return fmt.Sprintf("workspace selector %q is ambiguous", e.Selector)
}

// Workspace is the current projection of one durable workspace and its
// configured launch variants. DefaultLaunch is always the first config entry
// in the group; later entries remain independently launchable variants.
type Workspace struct {
	ID                  string
	ConfiguredProjectID string
	CanonicalPath       string
	DisplayName         string
	Aliases             []string
	DefaultLaunch       config.Session
	LaunchVariants      []config.Session
	TitleAliases        []string
	Worktrees           []WorktreeContext
}

// WorktreeContext identifies a repository checkout belonging to a workspace.
// The primary checkout is included so CWD mapping can distinguish it from
// linked worktrees without treating them as separate workspaces.
type WorktreeContext struct {
	ID            string
	WorkspaceID   string
	CanonicalPath string
	GitCommonDir  string
	Branch        string
	IsPrimary     bool
}

// CWDMatch maps a transcript CWD to a workspace and optional worktree. More
// than one configured launch variant can legitimately match one CWD, so all
// matching launch definitions are returned rather than selecting a profile.
type CWDMatch struct {
	Workspace Workspace
	Worktree  *WorktreeContext
	Projects  []config.Session
}

// Catalog is an immutable, synchronized view of the currently configured
// workspaces and Git topology. Its contents are deliberately private; callers
// receive deep copies so a retained catalog cannot be changed through config
// slices, maps, or returned workspace values.
type Catalog struct {
	workspaces   []Workspace
	synchronized bool
}

// Synchronized reports whether the catalog came from a successful
// Synchronize call. An empty configuration still produces a synchronized
// catalog, so len(Workspaces()) cannot represent this state.
func (c Catalog) Synchronized() bool {
	return c.synchronized
}

// Workspaces returns a deep copy of the catalog's current workspaces.
func (c Catalog) Workspaces() []Workspace {
	return cloneWorkspaces(c.workspaces)
}

// Service owns the datastore projection for configured workspaces.
type Service struct {
	store *datastore.Store
}

// New constructs a workspace service over an already-open datastore.
func New(store *datastore.Store) *Service {
	return &Service{store: store}
}

// Synchronize groups cfg's configured entries, persists stable IDs and
// historical metadata, reconciles the latest Git topology, and returns an
// immutable current catalog. Git-backed entries group by their shared Git
// common directory; non-Git entries group by their canonical physical
// directory.
func (s *Service) Synchronize(ctx context.Context, cfg *config.Config) (Catalog, error) {
	if s == nil || s.store == nil {
		return Catalog{}, errors.New("workspace: nil datastore")
	}
	if cfg == nil {
		return Catalog{}, errors.New("workspace: nil config")
	}

	projects := cfg.Projects
	// Config.Load normalizes the legacy [[sessions]] alias into Projects, but
	// callers such as embedded/TUI fixtures can construct Config directly.
	// Preserve the same configured-definition projection in that case.
	if len(projects) == 0 {
		projects = cfg.Sessions
	}
	defs, err := buildDefinitions(ctx, projects)
	if err != nil {
		return Catalog{}, err
	}
	if len(defs) == 0 {
		if err := s.store.Transaction(ctx, func(tx *sql.Tx) error {
			for _, statement := range []string{
				`DELETE FROM workspace_profile_memberships`,
				`DELETE FROM workspace_harness_memberships`,
			} {
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("workspace: clear empty search projection: %w", err)
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM search_documents WHERE entity_type = ?`, workspaceSearchEntityType); err != nil {
				return fmt.Errorf("workspace: clear empty search projection: %w", err)
			}
			return nil
		}); err != nil {
			return Catalog{}, err
		}
		return Catalog{synchronized: true}, nil
	}
	for i := range defs {
		defs[i].harness = configuredHarness(cfg, defs[i].projects[0].EffectiveProfile())
		for _, project := range defs[i].projects {
			profile := strings.TrimSpace(project.EffectiveProfile())
			defs[i].profiles = appendUniqueFold(defs[i].profiles, profile)
			defs[i].harnesses = appendUniqueFold(defs[i].harnesses, configuredHarness(cfg, profile))
		}
	}

	if err := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		for i := range defs {
			id, err := syncDefinition(ctx, tx, &defs[i])
			if err != nil {
				return err
			}
			defs[i].id = id
		}
		return removeStaleSearchDocuments(ctx, tx, defs)
	}); err != nil {
		return Catalog{}, err
	}

	workspaces := make([]Workspace, 0, len(defs))
	for _, def := range defs {
		persisted, err := loadWorkspace(ctx, s.store.DB(), def.id)
		if err != nil {
			return Catalog{}, err
		}
		workspaces = append(workspaces, def.workspace(persisted))
	}
	return Catalog{workspaces: cloneWorkspaces(workspaces), synchronized: true}, nil
}

// Build is the compatibility entry point for callers that want a synchronized
// workspace slice immediately. Long-running consumers should retain the
// Catalog returned by Synchronize and project from it without synchronizing on
// every poll.
func (s *Service) Build(ctx context.Context, cfg *config.Config) ([]Workspace, error) {
	catalog, err := s.Synchronize(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return catalog.Workspaces(), nil
}

// Resolve builds the current workspace projection then resolves selector by
// ID, canonical or historical path, or a unique title. ID takes precedence;
// title and path selectors only succeed when they identify exactly one row.
func (s *Service) Resolve(ctx context.Context, cfg *config.Config, selector string) (Workspace, error) {
	workspaces, err := s.Build(ctx, cfg)
	if err != nil {
		return Workspace{}, err
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Workspace{}, fmt.Errorf("%w: empty selector", ErrNotFound)
	}

	if matches := matchID(workspaces, selector); len(matches) > 0 {
		return oneWorkspace(selector, matches)
	}
	if looksLikePath(selector) {
		if matches := matchPath(workspaces, projectresolve.AbsPath(selector)); len(matches) > 0 {
			return oneWorkspace(selector, matches)
		}
	}
	return oneWorkspace(selector, matchTitle(workspaces, selector))
}

// MatchCWD uses projectresolve's Git-worktree-aware resolver to map a
// transcript CWD. The bool is false when the CWD is not under a configured
// project. A tied resolver result is acceptable when every match belongs to
// the same workspace; otherwise an AmbiguousSelectorError is returned.
func (s *Service) MatchCWD(ctx context.Context, cfg *config.Config, cwd string) (CWDMatch, bool, error) {
	workspaces, err := s.Build(ctx, cfg)
	if err != nil {
		return CWDMatch{}, false, err
	}
	projects := cfg.Projects
	if len(projects) == 0 {
		projects = cfg.Sessions
	}
	matches := projectresolve.NewWithGit(projects).Matches(cwd)
	if len(matches) == 0 {
		return CWDMatch{}, false, nil
	}

	byID := make(map[string]int, len(workspaces))
	for i := range workspaces {
		byID[workspaces[i].ID] = i
	}
	matched := make(map[string][]config.Session)
	for _, match := range matches {
		for _, workspace := range workspaces {
			if workspaceContainsProject(workspace, match.Project) {
				matched[workspace.ID] = appendUniqueProject(matched[workspace.ID], match.Project)
			}
		}
	}
	if len(matched) == 0 {
		return CWDMatch{}, false, nil
	}
	if len(matched) > 1 {
		candidates := make([]Candidate, 0, len(matched))
		for id := range matched {
			candidates = append(candidates, candidate(workspaces[byID[id]]))
		}
		sortCandidates(candidates)
		return CWDMatch{}, false, &AmbiguousSelectorError{Selector: cwd, Candidates: candidates}
	}
	for id, projects := range matched {
		workspace := workspaces[byID[id]]
		return CWDMatch{
			Workspace: workspace,
			Worktree:  workspaceWorktreeForCWD(workspace, cwd),
			Projects:  projects,
		}, true, nil
	}
	return CWDMatch{}, false, nil
}

type definition struct {
	id                  string
	key                 string
	canonicalPath       string
	configuredProjectID string
	displayName         string
	projects            []config.Session
	paths               []string
	titles              []string
	git                 *gitRepository
	harness             string
	profiles            []string
	harnesses           []string
}

func buildDefinitions(ctx context.Context, projects []config.Session) ([]definition, error) {
	byKey := make(map[string]int, len(projects))
	defs := make([]definition, 0, len(projects))
	for _, project := range projects {
		if project.Directory == "" {
			continue
		}
		physical := projectresolve.AbsPath(project.Directory)
		if physical == "" {
			continue
		}
		repo, found, err := discoverGitRepository(ctx, physical)
		if err != nil {
			return nil, err
		}
		key, canonical := "path:"+physical, physical
		if found {
			key, canonical = "git:"+repo.commonDir, repo.root
		}

		idx, ok := byKey[key]
		if !ok {
			idx = len(defs)
			byKey[key] = idx
			defs = append(defs, definition{
				key:                 key,
				canonicalPath:       canonical,
				configuredProjectID: project.ID,
				displayName:         project.Title,
				git:                 repoOrNil(repo, found),
			})
		}
		def := &defs[idx]
		def.projects = append(def.projects, project)
		def.paths = appendUniqueString(def.paths, physical)
		if canonical != physical {
			def.paths = appendUniqueString(def.paths, canonical)
		}
		def.titles = appendUniqueFold(def.titles, project.Title)
	}
	return defs, nil
}

func repoOrNil(repo gitRepository, found bool) *gitRepository {
	if !found {
		return nil
	}
	return &repo
}

func (d definition) workspace(p persistedWorkspace) Workspace {
	variants := cloneConfiguredSessions(d.projects[1:])
	return Workspace{
		ID:                  p.id,
		ConfiguredProjectID: p.configuredProjectID,
		CanonicalPath:       p.canonicalPath,
		DisplayName:         p.displayName,
		Aliases:             append([]string(nil), p.aliases...),
		DefaultLaunch:       cloneConfiguredSession(d.projects[0]),
		LaunchVariants:      variants,
		TitleAliases:        append([]string(nil), d.titles...),
		Worktrees:           d.currentWorktrees(p.worktrees),
	}
}

// currentWorktrees filters durable history through the latest Git discovery.
// Removed checkout rows remain available for historical references, but they
// are not part of the current catalog exposed to TUI and CLI consumers.
func (d definition) currentWorktrees(persisted []WorktreeContext) []WorktreeContext {
	if d.git == nil {
		return nil
	}
	current := make(map[string]bool, len(d.git.worktrees))
	for _, discovered := range d.git.worktrees {
		current[discovered.path] = true
	}
	worktrees := make([]WorktreeContext, 0, len(current))
	for _, context := range persisted {
		if current[context.CanonicalPath] {
			worktrees = append(worktrees, context)
		}
	}
	return worktrees
}

func cloneWorkspaces(in []Workspace) []Workspace {
	out := make([]Workspace, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Aliases = append([]string(nil), in[i].Aliases...)
		out[i].DefaultLaunch = cloneConfiguredSession(in[i].DefaultLaunch)
		out[i].LaunchVariants = cloneConfiguredSessions(in[i].LaunchVariants)
		out[i].TitleAliases = append([]string(nil), in[i].TitleAliases...)
		out[i].Worktrees = append([]WorktreeContext(nil), in[i].Worktrees...)
	}
	return out
}

type persistedWorkspace struct {
	id                  string
	canonicalPath       string
	displayName         string
	configuredProjectID string
	aliases             []string
	worktrees           []WorktreeContext
}

func syncDefinition(ctx context.Context, tx *sql.Tx, def *definition) (string, error) {
	existing, err := findExistingWorkspace(ctx, tx, *def)
	if err != nil {
		return "", err
	}
	id := ""
	oldCanonical := ""
	if existing != nil {
		id, oldCanonical = existing.id, existing.canonicalPath
	} else if def.configuredProjectID != "" {
		id = def.configuredProjectID
	} else if id, err = generatedID("ws"); err != nil {
		return "", err
	}

	now := time.Now().UnixNano()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (id, canonical_path, display_name, configured_project_id, created_at_ns, updated_at_ns)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			canonical_path = excluded.canonical_path,
			display_name = excluded.display_name,
			configured_project_id = excluded.configured_project_id,
			updated_at_ns = excluded.updated_at_ns
	`, id, def.canonicalPath, def.displayName, def.configuredProjectID, now, now); err != nil {
		return "", fmt.Errorf("workspace: upsert %q: %w", def.displayName, err)
	}

	aliases := append([]string(nil), def.paths...)
	if oldCanonical != "" && oldCanonical != def.canonicalPath {
		aliases = append(aliases, oldCanonical)
	}
	for _, alias := range aliases {
		if alias == "" || alias == def.canonicalPath {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO workspace_aliases (workspace_id, alias, created_at_ns)
			VALUES (?, ?, ?)
		`, id, alias, now); err != nil {
			return "", fmt.Errorf("workspace: add alias %q: %w", alias, err)
		}
	}

	if def.git != nil {
		for _, context := range def.git.worktrees {
			if err := upsertWorktreeContext(ctx, tx, id, def.git.commonDir, context, now); err != nil {
				return "", err
			}
		}
	}
	if err := upsertSearchDocument(ctx, tx, id, *def, now); err != nil {
		return "", err
	}
	if err := replaceSearchMemberships(ctx, tx, id, *def, now); err != nil {
		return "", err
	}
	// Preserve every discovery observation after this identity is explicitly
	// adopted instead of deleting its candidate history.
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_candidates SET workspace_id = ?, last_seen_at_ns = ?
		WHERE identity_key = ?
	`, id, now, def.key); err != nil {
		return "", fmt.Errorf("workspace: merge candidate history: %w", err)
	}
	return id, nil
}

func replaceSearchMemberships(ctx context.Context, tx *sql.Tx, id string, def definition, now int64) error {
	for _, statement := range []string{
		`DELETE FROM workspace_profile_memberships WHERE workspace_id = ?`,
		`DELETE FROM workspace_harness_memberships WHERE workspace_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return fmt.Errorf("workspace: clear search memberships for %q: %w", id, err)
		}
	}
	for position, profile := range def.profiles {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_profile_memberships (workspace_id, profile_name, position, created_at_ns)
			VALUES (?, ?, ?, ?)
		`, id, profile, position, now); err != nil {
			return fmt.Errorf("workspace: add profile membership %q for %q: %w", profile, id, err)
		}
	}
	for position, harnessName := range def.harnesses {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_harness_memberships (workspace_id, harness, position, created_at_ns)
			VALUES (?, ?, ?, ?)
		`, id, harnessName, position, now); err != nil {
			return fmt.Errorf("workspace: add harness membership %q for %q: %w", harnessName, id, err)
		}
	}
	return nil
}

func upsertSearchDocument(ctx context.Context, tx *sql.Tx, id string, def definition, now int64) error {
	content := make([]string, 0, len(def.titles)+len(def.paths)+len(def.projects)*4+3)
	content = append(content, def.displayName, def.configuredProjectID, def.canonicalPath)
	content = append(content, def.titles...)
	content = append(content, def.paths...)
	for _, project := range def.projects {
		content = append(content, project.ID, project.Title, project.Directory, project.EffectiveProfile())
		content = append(content, project.Groups...)
	}
	profile := ""
	if len(def.projects) > 0 {
		profile = def.projects[0].EffectiveProfile()
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO search_documents (
			entity_type, entity_id, workspace_id, harness, profile_name, occurred_at_ns,
			title, normalized_content, updated_at_ns
		) VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), 0, ?, ?, ?)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			harness = excluded.harness,
			profile_name = excluded.profile_name,
			title = excluded.title,
			normalized_content = excluded.normalized_content,
			updated_at_ns = excluded.updated_at_ns
		WHERE search_documents.workspace_id IS NOT excluded.workspace_id
			OR search_documents.harness IS NOT excluded.harness
			OR search_documents.profile_name IS NOT excluded.profile_name
			OR search_documents.title IS NOT excluded.title
			OR search_documents.normalized_content IS NOT excluded.normalized_content
	`, workspaceSearchEntityType, id, id, def.harness, profile, def.displayName, strings.Join(content, "\n"), now)
	if err != nil {
		return fmt.Errorf("workspace: upsert search document %q: %w", id, err)
	}
	return nil
}

func removeStaleSearchDocuments(ctx context.Context, tx *sql.Tx, defs []definition) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(defs)), ",")
	args := make([]any, 0, len(defs)+1)
	args = append(args, workspaceSearchEntityType)
	for _, def := range defs {
		args = append(args, def.id)
	}
	_, err := tx.ExecContext(ctx,
		`DELETE FROM search_documents WHERE entity_type = ? AND entity_id NOT IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("workspace: remove stale search documents: %w", err)
	}
	for _, table := range []string{"workspace_profile_memberships", "workspace_harness_memberships"} {
		membershipArgs := append([]any(nil), args[1:]...)
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE workspace_id NOT IN (`+placeholders+`)`,
			membershipArgs...,
		); err != nil {
			return fmt.Errorf("workspace: remove stale %s: %w", table, err)
		}
	}
	return nil
}

func configuredHarness(cfg *config.Config, profile string) string {
	if cfg == nil || profile == "" {
		return ""
	}
	profiles := cfg.Profiles
	if len(profiles) == 0 {
		profiles = cfg.Accounts
	}
	for _, candidate := range profiles {
		if strings.EqualFold(candidate.Name, profile) {
			return candidate.EffectiveHarness()
		}
	}
	return ""
}

func findExistingWorkspace(ctx context.Context, tx *sql.Tx, def definition) (*persistedWorkspace, error) {
	ids := map[string]bool{}
	add := func(query string, args ...any) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids[id] = true
		}
		return rows.Err()
	}
	if def.configuredProjectID != "" {
		if err := add(`SELECT id FROM workspaces WHERE lower(configured_project_id) = lower(?)`, def.configuredProjectID); err != nil {
			return nil, fmt.Errorf("workspace: find configured ID: %w", err)
		}
	}
	for _, path := range append(def.paths, def.canonicalPath) {
		if err := add(`
			SELECT id FROM workspaces WHERE canonical_path = ?
			UNION
			SELECT workspace_id FROM workspace_aliases WHERE alias = ?
		`, path, path); err != nil {
			return nil, fmt.Errorf("workspace: find path %q: %w", path, err)
		}
	}
	if def.git != nil {
		if err := add(`SELECT DISTINCT workspace_id FROM worktree_contexts WHERE git_common_dir = ?`, def.git.commonDir); err != nil {
			return nil, fmt.Errorf("workspace: find Git repository: %w", err)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 1 {
		return nil, fmt.Errorf("workspace: configuration for %q maps to multiple persisted workspaces", def.displayName)
	}
	for id := range ids {
		workspace, err := loadWorkspaceTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		return &workspace, nil
	}
	return nil, nil
}

func upsertWorktreeContext(ctx context.Context, tx *sql.Tx, workspaceID, commonDir string, discovered discoveredWorktree, now int64) error {
	var id, existingWorkspaceID string
	err := tx.QueryRowContext(ctx, `SELECT id, workspace_id FROM worktree_contexts WHERE canonical_path = ?`, discovered.path).Scan(&id, &existingWorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		var err error
		id, err = generatedID("wt")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO worktree_contexts (id, workspace_id, canonical_path, git_common_dir, branch, is_primary, created_at_ns, updated_at_ns)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, id, workspaceID, discovered.path, commonDir, discovered.branch, boolInt(discovered.primary), now, now); err != nil {
			return fmt.Errorf("workspace: add worktree %q: %w", discovered.path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("workspace: look up worktree %q: %w", discovered.path, err)
	}
	if existingWorkspaceID != workspaceID {
		return fmt.Errorf("workspace: worktree %q already belongs to workspace %q", discovered.path, existingWorkspaceID)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE worktree_contexts
		SET git_common_dir = ?, branch = ?, is_primary = ?, updated_at_ns = ?
		WHERE id = ?
	`, commonDir, discovered.branch, boolInt(discovered.primary), now, id); err != nil {
		return fmt.Errorf("workspace: update worktree %q: %w", discovered.path, err)
	}
	return nil
}

func loadWorkspace(ctx context.Context, db *sql.DB, id string) (persistedWorkspace, error) {
	return loadWorkspaceTx(ctx, db, id)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadWorkspaceTx(ctx context.Context, q queryer, id string) (persistedWorkspace, error) {
	var workspace persistedWorkspace
	if err := q.QueryRowContext(ctx, `
		SELECT id, canonical_path, display_name, configured_project_id
		FROM workspaces WHERE id = ?
	`, id).Scan(&workspace.id, &workspace.canonicalPath, &workspace.displayName, &workspace.configuredProjectID); err != nil {
		return persistedWorkspace{}, fmt.Errorf("workspace: load %q: %w", id, err)
	}
	aliases, err := q.QueryContext(ctx, `SELECT alias FROM workspace_aliases WHERE workspace_id = ? ORDER BY alias`, id)
	if err != nil {
		return persistedWorkspace{}, fmt.Errorf("workspace: load aliases: %w", err)
	}
	for aliases.Next() {
		var alias string
		if err := aliases.Scan(&alias); err != nil {
			_ = aliases.Close()
			return persistedWorkspace{}, fmt.Errorf("workspace: scan alias: %w", err)
		}
		workspace.aliases = append(workspace.aliases, alias)
	}
	if err := aliases.Close(); err != nil {
		return persistedWorkspace{}, fmt.Errorf("workspace: close aliases: %w", err)
	}
	contexts, err := q.QueryContext(ctx, `
		SELECT id, workspace_id, canonical_path, COALESCE(git_common_dir, ''), COALESCE(branch, ''), is_primary
		FROM worktree_contexts WHERE workspace_id = ?
		ORDER BY is_primary DESC, canonical_path
	`, id)
	if err != nil {
		return persistedWorkspace{}, fmt.Errorf("workspace: load worktrees: %w", err)
	}
	for contexts.Next() {
		var context WorktreeContext
		var primary int
		if err := contexts.Scan(&context.ID, &context.WorkspaceID, &context.CanonicalPath, &context.GitCommonDir, &context.Branch, &primary); err != nil {
			_ = contexts.Close()
			return persistedWorkspace{}, fmt.Errorf("workspace: scan worktree: %w", err)
		}
		context.IsPrimary = primary != 0
		workspace.worktrees = append(workspace.worktrees, context)
	}
	if err := contexts.Close(); err != nil {
		return persistedWorkspace{}, fmt.Errorf("workspace: close worktrees: %w", err)
	}
	return workspace, nil
}

func generatedID(prefix string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("workspace: generate ID: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func matchID(workspaces []Workspace, selector string) []Workspace {
	needle := config.NormalizeProjectID(selector)
	var matches []Workspace
	for _, workspace := range workspaces {
		if config.NormalizeProjectID(workspace.ID) == needle || config.NormalizeProjectID(workspace.ConfiguredProjectID) == needle || config.NormalizeProjectID(workspace.DefaultLaunch.ID) == needle {
			matches = append(matches, workspace)
			continue
		}
		for _, variant := range workspace.LaunchVariants {
			if config.NormalizeProjectID(variant.ID) == needle && needle != "" {
				matches = append(matches, workspace)
				break
			}
		}
	}
	return uniqueWorkspaces(matches)
}

func matchPath(workspaces []Workspace, selector string) []Workspace {
	var matches []Workspace
	for _, workspace := range workspaces {
		if workspace.CanonicalPath == selector || containsString(workspace.Aliases, selector) {
			matches = append(matches, workspace)
		}
	}
	return matches
}

func matchTitle(workspaces []Workspace, selector string) []Workspace {
	var matches []Workspace
	for _, workspace := range workspaces {
		if strings.EqualFold(workspace.DisplayName, selector) || containsFold(workspace.TitleAliases, selector) {
			matches = append(matches, workspace)
		}
	}
	return matches
}

func oneWorkspace(selector string, matches []Workspace) (Workspace, error) {
	matches = uniqueWorkspaces(matches)
	if len(matches) == 0 {
		return Workspace{}, fmt.Errorf("%w: %q", ErrNotFound, selector)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	candidates := make([]Candidate, 0, len(matches))
	for _, workspace := range matches {
		candidates = append(candidates, candidate(workspace))
	}
	sortCandidates(candidates)
	return Workspace{}, &AmbiguousSelectorError{Selector: selector, Candidates: candidates}
}

func candidate(workspace Workspace) Candidate {
	return Candidate{ID: workspace.ID, DisplayName: workspace.DisplayName, CanonicalPath: workspace.CanonicalPath}
}

func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].DisplayName != candidates[j].DisplayName {
			return candidates[i].DisplayName < candidates[j].DisplayName
		}
		return candidates[i].ID < candidates[j].ID
	})
}

func uniqueWorkspaces(in []Workspace) []Workspace {
	seen := make(map[string]bool, len(in))
	out := make([]Workspace, 0, len(in))
	for _, workspace := range in {
		if !seen[workspace.ID] {
			seen[workspace.ID] = true
			out = append(out, workspace)
		}
	}
	return out
}

func workspaceContainsProject(workspace Workspace, project config.Session) bool {
	if sameProject(workspace.DefaultLaunch, project) {
		return true
	}
	for _, variant := range workspace.LaunchVariants {
		if sameProject(variant, project) {
			return true
		}
	}
	return false
}

func sameProject(a, b config.Session) bool {
	if a.ID != "" || b.ID != "" {
		return a.ID != "" && b.ID != "" && strings.EqualFold(a.ID, b.ID)
	}
	return a.Title == b.Title && strings.EqualFold(a.EffectiveProfile(), b.EffectiveProfile()) && projectresolve.AbsPath(a.Directory) == projectresolve.AbsPath(b.Directory)
}

func appendUniqueProject(projects []config.Session, project config.Session) []config.Session {
	for _, existing := range projects {
		if sameProject(existing, project) {
			return projects
		}
	}
	return append(projects, project)
}

func workspaceWorktreeForCWD(workspace Workspace, cwd string) *WorktreeContext {
	abs := projectresolve.AbsPath(cwd)
	best := -1
	var found *WorktreeContext
	for i := range workspace.Worktrees {
		root := workspace.Worktrees[i].CanonicalPath
		if pathContains(root, abs) && len(root) > best {
			copy := workspace.Worktrees[i]
			found, best = &copy, len(root)
		}
	}
	return found
}

func pathContains(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	if root == path {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

func appendUniqueString(values []string, value string) []string {
	if value == "" || containsString(values, value) {
		return values
	}
	return append(values, value)
}

func appendUniqueFold(values []string, value string) []string {
	if value == "" || containsFold(values, value) {
		return values
	}
	return append(values, value)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func looksLikePath(selector string) bool {
	return filepath.IsAbs(selector) || strings.HasPrefix(selector, "~") || strings.HasPrefix(selector, ".") || strings.ContainsRune(selector, os.PathSeparator)
}

type gitRepository struct {
	root      string
	commonDir string
	worktrees []discoveredWorktree
}

type discoveredWorktree struct {
	path    string
	branch  string
	primary bool
}

func discoverGitRepository(ctx context.Context, dir string) (gitRepository, bool, error) {
	root, err := gitLine(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		if ctx.Err() != nil {
			return gitRepository{}, false, ctx.Err()
		}
		return gitRepository{}, false, nil
	}
	commonDir, err := gitLine(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if ctx.Err() != nil {
			return gitRepository{}, false, ctx.Err()
		}
		return gitRepository{}, false, nil
	}
	worktreeOutput, err := gitOutput(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		if ctx.Err() != nil {
			return gitRepository{}, false, ctx.Err()
		}
		return gitRepository{}, false, nil
	}
	worktrees, err := parseWorktrees(worktreeOutput)
	if err != nil {
		return gitRepository{}, false, nil
	}
	root = projectresolve.AbsPath(root)
	commonDir = projectresolve.AbsPath(commonDir)
	if root == "" || commonDir == "" || len(worktrees) == 0 {
		return gitRepository{}, false, nil
	}
	for i := range worktrees {
		worktrees[i].path = projectresolve.AbsPath(worktrees[i].path)
		if worktrees[i].path == "" {
			return gitRepository{}, false, nil
		}
		if projectresolve.AbsPath(filepath.Join(worktrees[i].path, ".git")) == commonDir {
			worktrees[i].primary = true
		}
	}
	primary := false
	for _, worktree := range worktrees {
		primary = primary || worktree.primary
	}
	if !primary {
		worktrees[0].primary = true
	}
	return gitRepository{root: root, commonDir: commonDir, worktrees: worktrees}, true, nil
}

func gitLine(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitOutput(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.Contains(line, "\n") {
		return "", errors.New("unexpected Git output")
	}
	return line, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func parseWorktrees(out []byte) ([]discoveredWorktree, error) {
	fields := strings.Split(string(out), "\x00")
	worktrees := make([]discoveredWorktree, 0)
	var current *discoveredWorktree
	finish := func() {
		if current != nil && current.path != "" {
			worktrees = append(worktrees, *current)
		}
		current = nil
	}
	for _, field := range fields {
		if field == "" {
			finish()
			continue
		}
		if strings.HasPrefix(field, "worktree ") {
			if current != nil {
				return nil, errors.New("malformed Git worktree list")
			}
			current = &discoveredWorktree{path: strings.TrimPrefix(field, "worktree ")}
			if current.path == "" {
				return nil, errors.New("empty Git worktree path")
			}
			continue
		}
		if current == nil {
			return nil, errors.New("missing Git worktree record")
		}
		if strings.HasPrefix(field, "branch ") {
			current.branch = strings.TrimPrefix(field, "branch refs/heads/")
		}
	}
	finish()
	if len(worktrees) == 0 {
		return nil, errors.New("empty Git worktree list")
	}
	return worktrees, nil
}
