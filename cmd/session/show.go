package session

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/spf13/cobra"
)

// sessionShowCmd prints what the index knows about one conversation. It
// resolves its selector exactly as continue and fork do — exact ID, unique
// prefix, harness-qualified or bare — so it is the way to check what one of
// those would act on, and its --json is the way to script around it (the
// directory to cd into, the transcript to open).
var sessionShowCmd = &cobra.Command{
	Use:               "show <harness> <session-id> | <harness>:<session-id> | <session-id>",
	ValidArgsFunction: cli.CompleteHarnessFirstArg,
	Short:             "Show one indexed conversation",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		identity, err := conversationIdentityFromArgs(cmd, jsonOutput, "show", args)
		if err != nil {
			return err
		}
		cfg, err := sessionCommandsLoadConfig()
		if err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
		}
		meta, err := resolveConversationOrFail(cmd, jsonOutput, cfg, sessionCommandsSessionReaders(), identity)
		if err != nil {
			return err
		}
		view := sessionViews([]transcript.SessionMeta{*meta})[0]
		if jsonOutput {
			return cli.WriteJSON(cmd, sessionShowData{Session: view})
		}
		return writeSessionShow(cmd.OutOrStdout(), view, sessionCommandsNow())
	},
}

type sessionShowData struct {
	Session sessionView `json:"session"`
}

// writeSessionShow lays the conversation out as labelled lines. Paths are
// printed whole rather than with ~, so each can be pasted anywhere.
func writeSessionShow(out io.Writer, view sessionView, now time.Time) error {
	dash := func(value string) string {
		if value == "" {
			return "-"
		}
		return value
	}
	when := func(at time.Time) string {
		if at.IsZero() {
			return "-"
		}
		return formatTime(at) + " (" + formatAgo(at, now) + ")"
	}
	rows := [][2]string{
		{"session", view.Harness + ":" + view.ID},
		{"profile", dash(strings.Join(view.Profiles, ", "))},
		{"project", dash(strings.Join(view.Projects, ", "))},
		{"directory", dash(view.CWD)},
		{"started", when(view.StartedAt)},
		{"active", when(view.LastAt)},
		{"messages", strconv.Itoa(view.MessageCount)},
		{"transcript", dash(view.Path)},
		{"summary", dash(oneLine(view.Snippet))},
	}
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "%-11s %s\n", row[0]+":", row[1])
	}
	_, err := io.WriteString(out, b.String())
	return err
}
