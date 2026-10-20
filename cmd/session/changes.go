package session

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/gitstate"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/spf13/cobra"
)

func newChangesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "changes", Short: "Review checkout changes for a live session or directory",
		Long: `Shows what changed in a Git checkout compared with --base (HEAD unless
given): staged and unstaged changes to tracked files, and untracked paths.
Name the checkout with exactly one of --directory, or --runtime-session for
the directory a live tmux session's agent is working in (a Kitty session is
reviewed by --directory). Nothing is written, and no Git hook, external diff
driver, or textconv runs.

Each changed file is listed as +added -deleted and its path; --file adds that
one file's patch. With --json it reports {root, base, files, patch}.`,
		Example: `  amx session changes --directory .
  amx session changes --directory ~/projects/api --base main
  amx session changes --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --file README.md
  amx session changes --directory . --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			dir, _ := cmd.Flags().GetString("directory")
			runtime, _ := cmd.Flags().GetString("runtime-session")
			base, _ := cmd.Flags().GetString("base")
			file, _ := cmd.Flags().GetString("file")
			if (dir == "") == (runtime == "") {
				return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("choose exactly one of --directory or --runtime-session"))
			}
			if runtime != "" {
				var err error
				dir, err = changesSessionDirectory(runtime)
				if err != nil {
					return cli.CommandFailure(cmd, jsonOutput, "session_unavailable", err)
				}
			}
			changes, err := gitstate.ReadChanges(cli.CommandContext(cmd), dir, base, file)
			if err != nil {
				return cli.CommandFailure(cmd, jsonOutput, "review_unavailable", err)
			}
			if jsonOutput {
				return cli.WriteJSON(cmd, changes)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s compared with %s\n", changes.Root, changes.Base)
			if err != nil {
				return err
			}
			for _, f := range changes.Files {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "+%d -%d\t%s\n", f.Added, f.Deleted, f.Path); err != nil {
					return err
				}
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), changes.Patch)
			return err
		},
	}
	cmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	cmd.Flags().String("directory", "", "checkout directory")
	cmd.Flags().String("runtime-session", "", "runtime session ID of a live tmux session whose working directory to review")
	cmd.Flags().String("base", "HEAD", "commit or reference to compare the working tree against")
	cmd.Flags().String("file", "", "changed path whose patch to include")
	return cmd
}

func init() { sessionCmd.AddCommand(newChangesCommand()) }

func changesSessionDirectory(raw string) (string, error) {
	id, err := runtimesession.ParseID(raw)
	if err != nil {
		return "", err
	}
	store, err := runtimesession.NewStore(attachRegistryPath())
	if err != nil {
		return "", err
	}
	registry, err := store.Load()
	if err != nil {
		return "", err
	}
	record, err := registry.Get(id)
	if err != nil {
		return "", err
	}
	if record.State != runtimesession.StateLive {
		return "", notLiveError(record)
	}
	if record.Target.Instance.Kind != runtimesession.BackendTmux {
		return "", errors.New("review this checkout by directory for a Kitty session")
	}
	opened, err := attachNewTmux()
	if err != nil {
		return "", err
	}
	server, ok := opened.(tmuxAttacher)
	if !ok {
		return "", errors.New("the backend cannot inspect this session")
	}
	survey, current, err := validateTmux(store, server, record)
	if err != nil {
		return "", err
	}
	if current.State != runtimesession.StateLive {
		return "", notLiveError(current)
	}
	pane, ok := backend.FindPane(survey.Panes, backend.PaneID(record.Target.Ref))
	if !ok || pane.CWD == "" {
		return "", errors.New("the session has no current checkout directory")
	}
	return pane.CWD, nil
}
