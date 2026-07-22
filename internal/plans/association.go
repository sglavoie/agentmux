package plans

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/projectresolve"
)

const (
	planMetadataFileName      = "plan-metadata.json"
	planMetadataSchemaVersion = 1
	prepareValidationFileName = "prepare-validation.json"
)

func discoverPlanCWD(planDir, stateRoot string) (string, error) {
	if stateRoot != "" {
		state, found, err := agentloop.LookupPlanState(planDir, stateRoot)
		if err != nil {
			return "", err
		}
		if found {
			return state.CWD, nil
		}
	}

	cwd, found, err := readPlanMetadataCWD(filepath.Join(planDir, planMetadataFileName))
	if err != nil || found {
		return cwd, err
	}
	cwd, _, err = readPrepareValidationCWD(filepath.Join(planDir, prepareValidationFileName))
	return cwd, err
}

func readPlanMetadataCWD(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("plans: reading workspace metadata %s: %w", path, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return "", false, fmt.Errorf("plans: decoding workspace metadata %s: %w", path, err)
	}
	unknown := make([]string, 0)
	for key := range fields {
		if key != "version" && key != "cwd" {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return "", false, fmt.Errorf("plans: workspace metadata %s has unknown fields: %s", path, strings.Join(unknown, ", "))
	}
	var version int
	if err := json.Unmarshal(fields["version"], &version); err != nil || version != planMetadataSchemaVersion {
		return "", false, fmt.Errorf("plans: workspace metadata %s has unsupported version", path)
	}
	var rawCWD string
	if err := json.Unmarshal(fields["cwd"], &rawCWD); err != nil {
		return "", false, fmt.Errorf("plans: workspace metadata %s has invalid cwd", path)
	}
	cwd, err := canonicalWorkspacePath(rawCWD)
	if err != nil {
		return "", false, fmt.Errorf("plans: workspace metadata %s: %w", path, err)
	}
	return cwd, true, nil
}

func readPrepareValidationCWD(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("plans: reading legacy preparation metadata %s: %w", path, err)
	}
	var payload struct {
		CodebaseRoot string `json:"codebase_root"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false, fmt.Errorf("plans: decoding legacy preparation metadata %s: %w", path, err)
	}
	cwd, err := canonicalWorkspacePath(payload.CodebaseRoot)
	if err != nil {
		return "", false, fmt.Errorf("plans: legacy preparation metadata %s: %w", path, err)
	}
	return cwd, true, nil
}

func canonicalWorkspacePath(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || !filepath.IsAbs(raw) {
		return "", errors.New("cwd must be a canonical absolute path")
	}
	canonical, err := agentloop.CanonicalizePath(raw)
	if err != nil {
		return "", fmt.Errorf("resolving cwd: %w", err)
	}
	if canonical != raw {
		return "", fmt.Errorf("cwd %q is not canonical (expected %q)", raw, canonical)
	}
	return canonical, nil
}

func classifyPlanProject(meta PlanMeta, resolver projectresolve.Resolver) PlanMeta {
	meta.Project = ""
	meta.Projects = nil
	if meta.CWD == "" {
		return meta
	}
	matches := resolver.Matches(meta.CWD)
	projects := make([]string, 0, len(matches))
	for _, match := range matches {
		projects = append(projects, match.Title)
	}
	projects = cleanProjectNames(projects)
	if len(projects) == 0 {
		return meta
	}
	meta.Project = projects[0]
	meta.Projects = projects
	return meta
}
