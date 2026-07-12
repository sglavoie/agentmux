package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/handoff"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

// Dependency seams make the read-only commands testable without a real user
// config, Kitty instance, or durable default datastore.
var (
	projectCommandsLoadConfig       = loadRunnableConfig
	projectCommandsOpenStore        = datastore.Open
	projectCommandsLoadPlanIndex    = plans.LoadSQLiteIndexWithStore
	projectCommandsLoadSessionIndex = transcript.LoadSQLiteIndexWithStore
	projectCommandsPlanIndexPath    = plans.IndexPath
	projectCommandsSessionIndexPath = transcript.IndexPath
	projectCommandsSessionReaders   = transcriptReaders
	projectCommandsNewKitty         = func() kitty.KittyClient { return kitty.NewClient(kitty.NewRealRunner()) }
	projectCommandsDaemon           = func(req daemon.Request) (daemon.Response, error) { return daemon.NewClient().Send(req) }
	projectCommandsNewHandoff       = func() handoff.Runner { return handoff.NewWorkflow() }
	projectCommandsNewWorktree      = func() worktree.Runner { return worktree.NewWorkflow() }
	projectCommandsConfigPath       = config.DefaultConfigPath
	projectCommandsReadFile         = os.ReadFile
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Inspect, discover, and adopt project workspaces",
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured workspaces",
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		workspaces, err := loadWorkspaces(commandContext(cmd))
		if err != nil {
			return commandFailure(cmd, jsonOutput, "workspace_unavailable", err)
		}
		if jsonOutput {
			return writeJSON(cmd, workspaceListData{Workspaces: workspaceViews(workspaces)})
		}
		for _, item := range workspaces {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", item.ID, item.DisplayName, item.CanonicalPath)
		}
		return nil
	},
}

var workspaceShowCmd = &cobra.Command{
	Use:   "show <workspace>",
	Short: "Show one configured workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return missingParameter(cmd, jsonOutput, "workspace", "workspace ID, configured title, or known path")
		}
		if len(args) > 1 {
			return commandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("workspace show accepts exactly one selector"))
		}

		cfg, err := projectCommandsLoadConfig()
		if err != nil {
			return commandFailure(cmd, jsonOutput, "config_error", err)
		}
		ctx := commandContext(cmd)
		store, err := projectCommandsOpenStore(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		item, err := workspace.New(store).Resolve(ctx, cfg, args[0])
		if err != nil {
			return workspaceCommandError(cmd, jsonOutput, err)
		}
		view := workspaceViewOf(item)
		if jsonOutput {
			return writeJSON(cmd, workspaceShowData{Workspace: view})
		}
		printWorkspace(cmd, view)
		return nil
	},
}

var workspaceDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "List useful unconfigured project candidates",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if len(args) != 0 {
			return commandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("workspace discover accepts no arguments"))
		}
		items, err := loadProjectCandidates(commandContext(cmd))
		if err != nil {
			return commandFailure(cmd, jsonOutput, "workspace_discovery_unavailable", err)
		}
		if jsonOutput {
			return writeJSON(cmd, workspaceCandidateListData{Candidates: items})
		}
		for _, item := range items {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", item.ID, item.DisplayName, item.CanonicalPath)
		}
		return nil
	},
}

var workspaceAdoptCmd = &cobra.Command{
	Use:   "adopt <candidate>",
	Short: "Preview and explicitly adopt a discovered project",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		profile, _ := cmd.Flags().GetString("profile")
		title, _ := cmd.Flags().GetString("title")
		confirmed, _ := cmd.Flags().GetBool("yes")
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return missingParameter(cmd, jsonOutput, "candidate", "candidate ID or canonical path from workspace discover")
		}
		if len(args) != 1 {
			return commandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("workspace adopt accepts exactly one candidate"))
		}
		if strings.TrimSpace(profile) == "" {
			return missingParameter(cmd, jsonOutput, "profile", "target configured profile")
		}

		cfg, err := projectCommandsLoadConfig()
		if err != nil {
			return commandFailure(cmd, jsonOutput, "config_error", err)
		}
		profileFound := false
		for _, configured := range cfg.Profiles {
			if strings.EqualFold(configured.Name, strings.TrimSpace(profile)) {
				profileFound = true
				break
			}
		}
		if !profileFound {
			return commandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("no matching profile %q", profile))
		}
		ctx := commandContext(cmd)
		store, err := projectCommandsOpenStore(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		if _, err := discoverProjectCandidates(ctx, cfg, store); err != nil {
			return commandFailure(cmd, jsonOutput, "workspace_discovery_unavailable", err)
		}
		candidate, err := workspace.New(store).ResolveCandidate(ctx, args[0])
		if err != nil {
			return workspaceCommandError(cmd, jsonOutput, err)
		}
		if strings.TrimSpace(title) == "" {
			title = candidate.DisplayName
		}
		project := config.Session{Title: title, Directory: candidate.CanonicalPath, Profile: profile}
		block, err := config.ProjectBlock(project)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "config_error", err)
		}
		if !confirmed {
			preview := workspaceAdoptionPreview{CandidateID: candidate.ID, Block: block, Confirmed: false}
			if jsonOutput {
				return writeJSON(cmd, preview)
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), block)
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Re-run with --yes to append this exact block.")
			return nil
		}
		path := projectCommandsConfigPath()
		original, err := projectCommandsReadFile(path)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "config_error", err)
		}
		request := actions.Request{
			Candidate:  &actions.ProjectCandidateTarget{ID: candidate.ID, CanonicalPath: candidate.CanonicalPath, DisplayName: candidate.DisplayName},
			Parameters: map[string]string{"profile": profile, "title": title}, Confirmed: true,
		}
		return executeSessionActionWithDependencies(cmd, jsonOutput, actions.Dependencies{
			Config: cfg, Adopter: config.NewFileAdopter(path, original),
		}, actions.AdoptProject, request)
	},
}

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Launch, focus, continue, fork, and hand off conversations",
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List indexed conversation sessions",
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		ctx := commandContext(cmd)
		store, err := projectCommandsOpenStore(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		idx, err := projectCommandsLoadSessionIndex(ctx, projectCommandsSessionIndexPath(), store, projectCommandsSessionReaders(), compatimport.SyncIfChanged)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		items := idx.List(transcript.ListFilter{})
		views := sessionViews(items)
		if jsonOutput {
			return writeJSON(cmd, sessionListData{Sessions: views})
		}
		for _, item := range views {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", item.Harness, item.Profile, item.Project, formatTime(item.LastAt), item.ID)
		}
		return nil
	},
}

var sessionLaunchCmd = &cobra.Command{
	Use:   "launch <workspace>",
	Short: "Launch a workspace's default configured project",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return missingParameter(cmd, jsonOutput, "workspace", "workspace ID, configured title, or known path")
		}
		if len(args) != 1 {
			return commandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("session launch accepts exactly one workspace selector"))
		}
		cfg, err := projectCommandsLoadConfig()
		if err != nil {
			return commandFailure(cmd, jsonOutput, "config_error", err)
		}
		store, err := projectCommandsOpenStore(commandContext(cmd))
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		workspaceItem, err := workspace.New(store).Resolve(commandContext(cmd), cfg, args[0])
		if err != nil {
			return workspaceCommandError(cmd, jsonOutput, err)
		}
		return executeSessionAction(cmd, jsonOutput, cfg, actions.Launch, actions.Request{Project: &workspaceItem.DefaultLaunch})
	},
}

var sessionFocusCmd = &cobra.Command{
	Use:   "focus <tab-id>",
	Short: "Focus one currently managed Kitty tab",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return missingParameter(cmd, jsonOutput, "tab-id", "current managed Kitty tab ID")
		}
		if len(args) != 1 {
			return commandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("session focus accepts exactly one tab ID"))
		}
		tabID, err := strconv.Atoi(args[0])
		if err != nil || tabID <= 0 {
			return commandFailure(cmd, jsonOutput, "invalid_selector", errors.New("tab-id must be a positive integer"))
		}
		client := projectCommandsNewKitty()
		windows, err := client.ListWindows()
		if err != nil {
			return commandFailure(cmd, jsonOutput, "live_lookup_unavailable", err)
		}
		var target *session.RunningSession
		for _, running := range session.ListFromOSWindows(windows) {
			if running.TabID == tabID {
				copy := running
				target = &copy
				break
			}
		}
		if target == nil {
			return commandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("managed tab %d was not found", tabID))
		}
		return executeSessionActionWithDependencies(cmd, jsonOutput, actions.Dependencies{Kitty: client}, actions.Focus, actions.Request{Session: target})
	},
}

var sessionContinueCmd = newConversationActionCommand("continue", actions.Continue, "Focus an exact live conversation or resume its indexed source")
var sessionForkCmd = newConversationActionCommand("fork", actions.Fork, "Fork an indexed conversation (portable for Copilot)")

var sessionForkWorktreeCmd = &cobra.Command{
	Use:   "fork-worktree <harness> <session-id>",
	Short: "Fork an indexed conversation in a new managed Git worktree",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		allowDirty, _ := cmd.Flags().GetBool("allow-dirty")
		confirmed, _ := cmd.Flags().GetBool("yes")
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return missingParameter(cmd, jsonOutput, "harness", "source harness (claude, codex, or copilot)")
		}
		if len(args) == 1 || strings.TrimSpace(args[1]) == "" {
			return missingParameter(cmd, jsonOutput, "session-id", "indexed conversation session ID")
		}
		if len(args) != 2 {
			return commandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("session fork-worktree accepts exactly a harness and session ID"))
		}
		identity, ok := conversation.New(args[0], args[1])
		if !ok {
			return commandFailure(cmd, jsonOutput, "invalid_selector", errors.New("harness and session-id must be non-empty"))
		}
		cfg, err := projectCommandsLoadConfig()
		if err != nil {
			return commandFailure(cmd, jsonOutput, "config_error", err)
		}
		readers := projectCommandsSessionReaders()
		ctx := commandContext(cmd)
		store, err := projectCommandsOpenStore(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		idx, err := projectCommandsLoadSessionIndex(ctx, projectCommandsSessionIndexPath(), store, readers, compatimport.SyncIfChanged)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		meta, found := idx.Get(transcript.IndexKey(identity.Harness, identity.SessionID))
		if !found || meta.HiddenFromIndex {
			return commandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s:%s was not found", identity.Harness, identity.SessionID))
		}
		parameters := map[string]string{}
		if allowDirty {
			parameters["allow_dirty"] = "true"
		}
		return executeSessionAction(cmd, jsonOutput, cfg, actions.ForkWorktree, actions.Request{Conversation: &meta, Parameters: parameters, Confirmed: confirmed})
	},
}

var sessionHandoffCmd = &cobra.Command{
	Use:   "handoff <conversation>",
	Short: "Launch a fresh target profile from a portable handoff pack",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		edit, _ := cmd.Flags().GetBool("edit")
		noLaunch, _ := cmd.Flags().GetBool("no-launch")
		profile, _ := cmd.Flags().GetString("profile")
		if edit && jsonOutput {
			return commandFailure(cmd, jsonOutput, "invalid_options", errors.New("--edit cannot be used with --json"))
		}
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return missingParameter(cmd, jsonOutput, "conversation", "indexed conversation as <harness>:<session-id>")
		}
		if len(args) != 1 {
			return commandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("session handoff accepts exactly one conversation selector"))
		}
		if strings.TrimSpace(profile) == "" {
			return missingParameter(cmd, jsonOutput, "profile", "target configured profile")
		}
		identity, ok := parseConversationSelector(args[0])
		if !ok {
			return commandFailure(cmd, jsonOutput, "invalid_selector", errors.New("conversation must be formatted as <harness>:<session-id>"))
		}
		cfg, err := projectCommandsLoadConfig()
		if err != nil {
			return commandFailure(cmd, jsonOutput, "config_error", err)
		}
		readers := projectCommandsSessionReaders()
		ctx := commandContext(cmd)
		store, err := projectCommandsOpenStore(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		idx, err := projectCommandsLoadSessionIndex(ctx, projectCommandsSessionIndexPath(), store, readers, compatimport.SyncIfChanged)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "session_index_unavailable", err)
		}
		meta, found := idx.Get(transcript.IndexKey(identity.Harness, identity.SessionID))
		if !found || meta.HiddenFromIndex {
			return commandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s:%s was not found", identity.Harness, identity.SessionID))
		}
		parameters := map[string]string{"profile": profile}
		if edit {
			parameters["edit"] = "true"
		}
		if noLaunch {
			parameters["no_launch"] = "true"
		}
		return executeSessionAction(cmd, jsonOutput, cfg, actions.Handoff, actions.Request{Conversation: &meta, Parameters: parameters})
	},
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Inspect and explicitly link indexed plans",
}

var planListCmd = &cobra.Command{
	Use:   "list",
	Short: "List indexed plans",
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		ctx := commandContext(cmd)
		store, err := projectCommandsOpenStore(ctx)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "datastore_error", err)
		}
		defer func() { _ = store.Close() }()
		idx, err := projectCommandsLoadPlanIndex(ctx, projectCommandsPlanIndexPath(), store, compatimport.SyncIfChanged)
		if err != nil {
			return commandFailure(cmd, jsonOutput, "plan_index_unavailable", err)
		}
		items := idx.List()
		views := planViews(items)
		if jsonOutput {
			return writeJSON(cmd, planListData{Plans: views})
		}
		for _, item := range views {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", formatTime(item.ModTime), item.Name, item.DirPath)
		}
		return nil
	},
}

var planLinkCmd = newPlanWorkspaceCommand("link", true)
var planUnlinkCmd = newPlanWorkspaceCommand("unlink", false)

func newPlanWorkspaceCommand(name string, link bool) *cobra.Command {
	short := "Link one indexed plan to a configured workspace"
	if !link {
		short = "Remove one explicit plan-to-workspace link"
	}
	return &cobra.Command{
		Use:   name + " <plan> <workspace>",
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
				return missingParameter(cmd, jsonOutput, "plan", "exact plan directory path or unique indexed plan name")
			}
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return missingParameter(cmd, jsonOutput, "workspace", "workspace ID, configured title, or known path")
			}
			if len(args) != 2 {
				return commandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("plan %s accepts exactly a plan and workspace selector", name))
			}
			ctx := commandContext(cmd)
			store, err := projectCommandsOpenStore(ctx)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "datastore_error", err)
			}
			defer func() { _ = store.Close() }()
			idx, err := projectCommandsLoadPlanIndex(ctx, projectCommandsPlanIndexPath(), store, compatimport.SyncIfChanged)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "plan_index_unavailable", err)
			}
			plan, err := resolveIndexedPlan(idx.List(), args[0])
			if err != nil {
				return planCommandError(cmd, jsonOutput, args[0], err)
			}
			cfg, err := projectCommandsLoadConfig()
			if err != nil {
				return commandFailure(cmd, jsonOutput, "config_error", err)
			}
			workspaceItem, err := workspace.New(store).Resolve(ctx, cfg, args[1])
			if err != nil {
				return workspaceCommandError(cmd, jsonOutput, err)
			}
			if link {
				err = idx.LinkWorkspace(plans.PlanKey(plan.DirPath), workspaceItem.ID)
			} else {
				err = idx.UnlinkWorkspace(plans.PlanKey(plan.DirPath), workspaceItem.ID)
			}
			if err != nil {
				return commandFailure(cmd, jsonOutput, "plan_link_failed", err)
			}
			result := planWorkspaceLinkData{Plan: planViewOf(plan), Workspace: workspaceViewOf(workspaceItem), Linked: link}
			if jsonOutput {
				return writeJSON(cmd, result)
			}
			verb, preposition := "linked", "to"
			if !link {
				verb, preposition = "unlinked", "from"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s workspace %s\n", verb, plan.DirPath, preposition, workspaceItem.ID)
			return nil
		},
	}
}

func init() {
	workspaceListCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	workspaceShowCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	workspaceDiscoverCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	workspaceAdoptCmd.Flags().String("profile", "", "target configured profile (required)")
	workspaceAdoptCmd.Flags().String("title", "", "project title (defaults to directory basename)")
	workspaceAdoptCmd.Flags().Bool("yes", false, "confirm appending the previewed block")
	workspaceAdoptCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionListCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionLaunchCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionFocusCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionContinueCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionForkCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionForkWorktreeCmd.Flags().Bool("allow-dirty", false, "allow a dirty source; uncommitted changes are not copied")
	sessionForkWorktreeCmd.Flags().Bool("yes", false, "confirm creating the isolated fork")
	sessionForkWorktreeCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionHandoffCmd.Flags().String("profile", "", "target configured profile")
	sessionHandoffCmd.Flags().Bool("edit", false, "edit the generated handoff before launch")
	sessionHandoffCmd.Flags().Bool("no-launch", false, "finalize the handoff without launching a target")
	sessionHandoffCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	planListCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	planLinkCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	planUnlinkCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	workspaceCmd.AddCommand(workspaceListCmd, workspaceShowCmd, workspaceDiscoverCmd, workspaceAdoptCmd)
	sessionCmd.AddCommand(sessionListCmd, sessionLaunchCmd, sessionFocusCmd, sessionContinueCmd, sessionForkCmd, sessionForkWorktreeCmd, sessionHandoffCmd)
	planCmd.AddCommand(planListCmd, planLinkCmd, planUnlinkCmd)
}

type workspaceCandidateListData struct {
	Candidates []workspace.ProjectCandidate `json:"candidates"`
}

type workspaceAdoptionPreview struct {
	CandidateID string `json:"candidate_id"`
	Block       string `json:"block"`
	Confirmed   bool   `json:"confirmed"`
}

func loadProjectCandidates(ctx context.Context) ([]workspace.ProjectCandidate, error) {
	cfg, err := projectCommandsLoadConfig()
	if err != nil {
		return nil, err
	}
	store, err := projectCommandsOpenStore(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return discoverProjectCandidates(ctx, cfg, store)
}

func discoverProjectCandidates(ctx context.Context, cfg *config.Config, store *datastore.Store) ([]workspace.ProjectCandidate, error) {
	var live []session.RunningSession
	if client := projectCommandsNewKitty(); client != nil {
		if windows, err := client.ListWindows(); err == nil {
			live = session.ListFromOSWindows(windows)
		}
	}
	var conversations []transcript.SessionMeta
	if idx, err := projectCommandsLoadSessionIndex(ctx, projectCommandsSessionIndexPath(), store, projectCommandsSessionReaders(), compatimport.SyncIfChanged); err == nil {
		conversations = idx.List(transcript.ListFilter{})
	}
	var evidence []workspace.PlanEvidence
	if idx, err := projectCommandsLoadPlanIndex(ctx, projectCommandsPlanIndexPath(), store, compatimport.SyncIfChanged); err == nil {
		evidence = collectPlanEvidence(idx.List(), projectCommandsReadFile)
	}
	return workspace.New(store).DiscoverCandidates(ctx, workspace.DiscoveryOptions{
		Config: cfg, LiveSessions: live, Conversations: conversations, Plans: evidence,
	})
}

func collectPlanEvidence(items []plans.PlanMeta, readFile func(string) ([]byte, error)) []workspace.PlanEvidence {
	evidence := make([]workspace.PlanEvidence, 0, len(items))
	for _, plan := range items {
		var content strings.Builder
		for _, relative := range plan.Files {
			path := filepath.Join(plan.DirPath, relative)
			data, err := readFile(path)
			if err != nil {
				continue
			}
			if len(data) > 2<<20 {
				data = data[:2<<20]
			}
			if content.Len()+len(data) > 4<<20 {
				data = data[:max(0, (4<<20)-content.Len())]
			}
			content.Write(data)
			content.WriteByte('\n')
			if content.Len() >= 4<<20 {
				break
			}
		}
		evidence = append(evidence, workspace.PlanEvidence{ID: plans.PlanKey(plan.DirPath), Name: plan.Name, Content: content.String()})
	}
	return evidence
}

func newConversationActionCommand(name string, actionID actions.ID, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <harness> <session-id>",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
				return missingParameter(cmd, jsonOutput, "harness", "source harness (claude, codex, or copilot)")
			}
			if len(args) == 1 || strings.TrimSpace(args[1]) == "" {
				return missingParameter(cmd, jsonOutput, "session-id", "indexed conversation session ID")
			}
			if len(args) != 2 {
				return commandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("session %s accepts exactly a harness and session ID", name))
			}
			identity, ok := conversation.New(args[0], args[1])
			if !ok {
				return commandFailure(cmd, jsonOutput, "invalid_selector", errors.New("harness and session-id must be non-empty"))
			}
			cfg, err := projectCommandsLoadConfig()
			if err != nil {
				return commandFailure(cmd, jsonOutput, "config_error", err)
			}
			readers := projectCommandsSessionReaders()
			ctx := commandContext(cmd)
			store, err := projectCommandsOpenStore(ctx)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "datastore_error", err)
			}
			defer func() { _ = store.Close() }()
			idx, err := projectCommandsLoadSessionIndex(ctx, projectCommandsSessionIndexPath(), store, readers, compatimport.SyncIfChanged)
			if err != nil {
				return commandFailure(cmd, jsonOutput, "session_index_unavailable", err)
			}
			meta, found := idx.Get(transcript.IndexKey(identity.Harness, identity.SessionID))
			if !found || meta.HiddenFromIndex {
				return commandFailure(cmd, jsonOutput, "not_found", fmt.Errorf("indexed conversation %s:%s was not found", identity.Harness, identity.SessionID))
			}
			return executeSessionAction(cmd, jsonOutput, cfg, actionID, actions.Request{Conversation: &meta})
		},
	}
}

func executeSessionAction(cmd *cobra.Command, jsonOutput bool, cfg *config.Config, actionID actions.ID, request actions.Request) error {
	return executeSessionActionWithDependencies(cmd, jsonOutput, actions.Dependencies{
		Kitty: projectCommandsNewKitty(), Config: cfg, Readers: projectCommandsSessionReaders(), Daemon: projectCommandsDaemon, Handoff: projectCommandsNewHandoff(), Worktree: projectCommandsNewWorktree(),
	}, actionID, request)
}

func executeSessionActionWithDependencies(cmd *cobra.Command, jsonOutput bool, deps actions.Dependencies, actionID actions.ID, request actions.Request) error {
	result, err := actions.DefaultRegistry().Execute(commandContext(cmd), deps, actionID, request)
	if err != nil {
		return sessionActionCommandError(cmd, jsonOutput, result, err)
	}
	if jsonOutput {
		return writeJSON(cmd, sessionActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data})
	}
	if result.Message != "" {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
	}
	return nil
}

func sessionActionCommandError(cmd *cobra.Command, jsonOutput bool, result actions.Result, err error) error {
	var partial *actions.PartialResultError
	if errors.As(err, &partial) {
		if jsonOutput {
			return writeJSONPartial(cmd, sessionActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data}, partial)
		}
		if result.Message != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
		}
		return partial
	}
	if !jsonOutput {
		return err
	}
	var actionErr *actions.Error
	if errors.As(err, &actionErr) {
		return writeJSONError(cmd, JSONError{Code: actionErr.Code, Message: actionErr.Message})
	}
	return commandFailure(cmd, true, "action_failed", err)
}

func parseConversationSelector(value string) (conversation.Identity, bool) {
	harnessName, sessionID, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found {
		return conversation.Identity{}, false
	}
	return conversation.New(harnessName, sessionID)
}

func loadWorkspaces(ctx context.Context) ([]workspace.Workspace, error) {
	cfg, err := projectCommandsLoadConfig()
	if err != nil {
		return nil, err
	}
	store, err := projectCommandsOpenStore(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return workspace.New(store).Build(ctx, cfg)
}

func commandContext(cmd *cobra.Command) context.Context {
	if cmd != nil && cmd.Context() != nil {
		return cmd.Context()
	}
	return context.Background()
}

func workspaceCommandError(cmd *cobra.Command, jsonOutput bool, err error) error {
	if !jsonOutput {
		return err
	}
	var ambiguous *workspace.AmbiguousSelectorError
	if errors.As(err, &ambiguous) {
		candidates := make([]Candidate, 0, len(ambiguous.Candidates))
		for _, item := range ambiguous.Candidates {
			candidates = append(candidates, Candidate{ID: item.ID, DisplayName: item.DisplayName, CanonicalPath: item.CanonicalPath})
		}
		return writeJSONError(cmd, JSONError{Code: "ambiguous_selector", Message: err.Error(), Candidates: candidates})
	}
	if errors.Is(err, workspace.ErrNotFound) {
		return writeJSONError(cmd, JSONError{Code: "not_found", Message: err.Error()})
	}
	return commandFailure(cmd, true, "workspace_unavailable", err)
}

type workspaceListData struct {
	Workspaces []workspaceView `json:"workspaces"`
}

type workspaceShowData struct {
	Workspace workspaceView `json:"workspace"`
}

type workspaceView struct {
	ID                  string         `json:"id"`
	ConfiguredProjectID string         `json:"configured_project_id,omitempty"`
	DisplayName         string         `json:"display_name"`
	CanonicalPath       string         `json:"canonical_path"`
	Aliases             []string       `json:"aliases"`
	DefaultLaunch       projectView    `json:"default_launch"`
	LaunchVariants      []projectView  `json:"launch_variants"`
	TitleAliases        []string       `json:"title_aliases"`
	Worktrees           []worktreeView `json:"worktrees"`
}

type projectView struct {
	ID        string            `json:"id,omitempty"`
	Title     string            `json:"title"`
	Directory string            `json:"directory"`
	Profile   string            `json:"profile"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Groups    []string          `json:"groups,omitempty"`
}

type worktreeView struct {
	ID            string `json:"id"`
	CanonicalPath string `json:"canonical_path"`
	GitCommonDir  string `json:"git_common_dir,omitempty"`
	Branch        string `json:"branch,omitempty"`
	IsPrimary     bool   `json:"is_primary"`
}

func workspaceViews(items []workspace.Workspace) []workspaceView {
	out := make([]workspaceView, 0, len(items))
	for _, item := range items {
		out = append(out, workspaceViewOf(item))
	}
	return out
}

func workspaceViewOf(item workspace.Workspace) workspaceView {
	variants := make([]projectView, 0, len(item.LaunchVariants))
	for _, launch := range item.LaunchVariants {
		variants = append(variants, projectViewOf(launch))
	}
	worktrees := make([]worktreeView, 0, len(item.Worktrees))
	for _, worktree := range item.Worktrees {
		worktrees = append(worktrees, worktreeView{ID: worktree.ID, CanonicalPath: worktree.CanonicalPath, GitCommonDir: worktree.GitCommonDir, Branch: worktree.Branch, IsPrimary: worktree.IsPrimary})
	}
	return workspaceView{
		ID: item.ID, ConfiguredProjectID: item.ConfiguredProjectID, DisplayName: item.DisplayName, CanonicalPath: item.CanonicalPath,
		Aliases: append([]string{}, item.Aliases...), DefaultLaunch: projectViewOf(item.DefaultLaunch), LaunchVariants: variants,
		TitleAliases: append([]string{}, item.TitleAliases...), Worktrees: worktrees,
	}
}

func projectViewOf(project config.Session) projectView {
	return projectView{ID: project.ID, Title: project.Title, Directory: project.Directory, Profile: project.EffectiveProfile(), Args: append([]string{}, project.EffectiveArgs()...), Env: cloneStrings(project.Env), Groups: append([]string{}, project.Groups...)}
}

func cloneStrings(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func printWorkspace(cmd *cobra.Command, item workspaceView) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "id: %s\nname: %s\npath: %s\n", item.ID, item.DisplayName, item.CanonicalPath)
	for _, launch := range append([]projectView{item.DefaultLaunch}, item.LaunchVariants...) {
		_, _ = fmt.Fprintf(out, "launch: %s\t%s\t%s\n", launch.Profile, launch.Title, launch.Directory)
	}
	for _, worktree := range item.Worktrees {
		_, _ = fmt.Fprintf(out, "worktree: %s\t%s\t%t\n", worktree.Branch, worktree.CanonicalPath, worktree.IsPrimary)
	}
}

type sessionListData struct {
	Sessions []sessionView `json:"sessions"`
}

type sessionActionData struct {
	Action  actions.ID     `json:"action"`
	Changed bool           `json:"changed"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

type sessionView struct {
	ID           string    `json:"id"`
	Harness      string    `json:"harness"`
	Profile      string    `json:"profile"`
	Profiles     []string  `json:"profiles"`
	Project      string    `json:"project"`
	CWD          string    `json:"cwd"`
	StartedAt    time.Time `json:"started_at"`
	LastAt       time.Time `json:"last_at"`
	Path         string    `json:"path"`
	FileSize     int64     `json:"file_size"`
	FileModTime  time.Time `json:"file_mod_time"`
	MessageCount int       `json:"message_count"`
	Snippet      string    `json:"snippet"`
}

func sessionViews(items []transcript.SessionMeta) []sessionView {
	out := make([]sessionView, 0, len(items))
	for _, item := range items {
		out = append(out, sessionView{ID: item.ID, Harness: item.Harness, Profile: item.DisplayProfile(), Profiles: append([]string{}, item.ProfileNames()...), Project: item.Project, CWD: item.CWD, StartedAt: item.StartedAt, LastAt: item.LastAt, Path: item.Path, FileSize: item.FileSize, FileModTime: item.FileModTime, MessageCount: item.MessageCount, Snippet: item.Snippet})
	}
	return out
}

type planListData struct {
	Plans []planView `json:"plans"`
}

type planWorkspaceLinkData struct {
	Plan      planView      `json:"plan"`
	Workspace workspaceView `json:"workspace"`
	Linked    bool          `json:"linked"`
}

type ambiguousPlanError struct {
	Candidates []plans.PlanMeta
}

func (e *ambiguousPlanError) Error() string { return "plan selector is ambiguous" }

func resolveIndexedPlan(items []plans.PlanMeta, selector string) (plans.PlanMeta, error) {
	selector = strings.TrimSpace(selector)
	for _, item := range items {
		if selector == item.DirPath {
			return item, nil
		}
	}
	matches := make([]plans.PlanMeta, 0)
	for _, item := range items {
		if strings.EqualFold(selector, item.Name) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool { return matches[i].DirPath < matches[j].DirPath })
		return plans.PlanMeta{}, &ambiguousPlanError{Candidates: matches}
	}
	return plans.PlanMeta{}, fmt.Errorf("indexed plan %q was not found", selector)
}

func planCommandError(cmd *cobra.Command, jsonOutput bool, selector string, err error) error {
	if !jsonOutput {
		return err
	}
	var ambiguous *ambiguousPlanError
	if errors.As(err, &ambiguous) {
		candidates := make([]Candidate, 0, len(ambiguous.Candidates))
		for _, item := range ambiguous.Candidates {
			candidates = append(candidates, Candidate{ID: item.DirPath, DisplayName: item.Name, CanonicalPath: item.DirPath})
		}
		return writeJSONError(cmd, JSONError{Code: "ambiguous_selector", Message: fmt.Sprintf("plan selector %q is ambiguous", selector), Candidates: candidates})
	}
	return writeJSONError(cmd, JSONError{Code: "not_found", Message: err.Error()})
}

type planView struct {
	Name    string    `json:"name"`
	DirPath string    `json:"dir_path"`
	Files   []string  `json:"files"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Snippet string    `json:"snippet"`
}

func planViews(items []plans.PlanMeta) []planView {
	out := make([]planView, 0, len(items))
	for _, item := range items {
		out = append(out, planView{Name: item.Name, DirPath: item.DirPath, Files: append([]string{}, item.Files...), Size: item.Size, ModTime: item.ModTime, Snippet: item.Snippet})
	}
	return out
}

func planViewOf(item plans.PlanMeta) planView {
	return planView{Name: item.Name, DirPath: item.DirPath, Files: append([]string{}, item.Files...), Size: item.Size, ModTime: item.ModTime, Snippet: item.Snippet}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
