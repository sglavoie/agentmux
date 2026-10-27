package actions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func accountByName(cfg *config.Config, profile string) (config.Account, bool) {
	if cfg == nil {
		return config.Account{}, false
	}
	accounts := cfg.Accounts
	if len(accounts) == 0 {
		accounts = cfg.Profiles
	}
	for _, account := range accounts {
		if strings.EqualFold(account.Name, profile) {
			return account, true
		}
	}
	return config.Account{}, false
}

func requireProjectAccount(id ID, deps Dependencies, project config.Session) (config.Account, error) {
	if deps.Config == nil {
		return config.Account{}, dependencyError(id, "configuration")
	}
	account, ok := accountByName(deps.Config, project.EffectiveProfile())
	if !ok {
		return config.Account{}, &Error{Code: "not_found", Action: id, Message: fmt.Sprintf("no matching profile %q", project.EffectiveProfile())}
	}
	return account, nil
}

func parameterBool(parameters map[string]string, name string) bool {
	value := strings.TrimSpace(parameters[name])
	return strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes")
}

func conversationIdentity(meta transcript.SessionMeta, action ID) (conversation.Identity, error) {
	identity, ok := conversation.New(meta.Harness, meta.ID)
	if !ok {
		return conversation.Identity{}, &Error{Code: "invalid_conversation", Action: action, Message: "indexed conversation must include a harness and session ID"}
	}
	return identity, nil
}

// continuityLaunchTarget validates the immutable source fields Agent Mux needs
// to resume safely, then derives a configured launch template without changing
// the source CWD/profile/harness. A matching configured project carries its
// explicit args and environment forward only when it is uniquely identified.
func continuityLaunchTarget(deps Dependencies, meta transcript.SessionMeta, identity conversation.Identity, action ID) (config.Session, config.Account, error) {
	if err := validateIndexedSource(meta, identity, deps.Readers); err != nil {
		return config.Session{}, config.Account{}, &Error{Code: "source_unavailable", Action: action, Message: err.Error()}
	}
	profile := strings.TrimSpace(meta.DisplayProfile())
	if profile == "" {
		return config.Session{}, config.Account{}, &Error{Code: "invalid_conversation", Action: action, Message: "indexed conversation has no originating profile"}
	}
	account, ok := accountByName(deps.Config, profile)
	if !ok {
		return config.Session{}, config.Account{}, &Error{Code: "not_found", Action: action, Message: fmt.Sprintf("no matching profile %q", profile)}
	}
	if !strings.EqualFold(account.EffectiveHarness(), identity.Harness) {
		return config.Session{}, config.Account{}, &Error{
			Code:    actionCodeProfileHarnessMismatch,
			Action:  action,
			Message: fmt.Sprintf("profile %q now uses %q, but the indexed conversation belongs to %q", account.Name, account.EffectiveHarness(), identity.Harness),
		}
	}
	return continuityProject(deps.Config, meta, account), account, nil
}

const actionCodeProfileHarnessMismatch = "profile_harness_mismatch"

func validateIndexedSource(meta transcript.SessionMeta, identity conversation.Identity, readers map[string]transcript.Reader) error {
	if strings.TrimSpace(meta.Path) == "" {
		return errors.New("indexed conversation has no transcript source path")
	}
	if strings.TrimSpace(meta.CWD) == "" {
		return errors.New("indexed conversation has no source working directory")
	}
	reader, ok := readers[identity.Harness]
	if !ok || reader == nil {
		return fmt.Errorf("no transcript reader is registered for harness %q", identity.Harness)
	}
	if !strings.EqualFold(reader.Harness(), identity.Harness) {
		return fmt.Errorf("transcript reader %q cannot validate harness %q", reader.Harness(), identity.Harness)
	}
	info, err := os.Stat(meta.Path)
	if err != nil {
		return fmt.Errorf("reading indexed transcript source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("indexed transcript source %q is not a regular file", meta.Path)
	}
	if _, err := reader.ReadMessages(meta.Path); err != nil {
		return fmt.Errorf("validating indexed transcript source: %w", err)
	}
	return nil
}

func continuityProject(cfg *config.Config, meta transcript.SessionMeta, account config.Account) config.Session {
	var candidates []config.Session
	for _, project := range cfg.Projects {
		if !strings.EqualFold(project.EffectiveProfile(), account.Name) || !sameSourceDirectory(project.Directory, meta.CWD) {
			continue
		}
		if meta.Project != "" && strings.EqualFold(project.Title, meta.Project) {
			project.Directory = meta.CWD
			project.Profile, project.Account = account.Name, account.Name
			return project
		}
		candidates = append(candidates, project)
	}
	if len(candidates) == 1 {
		project := candidates[0]
		project.Directory = meta.CWD
		project.Profile, project.Account = account.Name, account.Name
		return project
	}
	title := strings.TrimSpace(meta.Project)
	if title == "" {
		title = meta.ID
	}
	return config.Session{Title: title, Directory: meta.CWD, Profile: account.Name, Account: account.Name}
}

func sameSourceDirectory(left, right string) bool {
	left = filepath.Clean(config.ExpandTilde(left))
	right = filepath.Clean(config.ExpandTilde(right))
	return left != "." && left == right
}

func continuityLaunchError(action ID, err error) error {
	if errors.Is(err, harness.ErrUnsupportedInvocation) {
		return &Error{Code: "capability_unavailable", Action: action, Message: err.Error()}
	}
	return &Error{Code: "launch_failed", Action: action, Message: err.Error(), Cause: err}
}
