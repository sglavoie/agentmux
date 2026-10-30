package dashboard

import (
	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui"
)

// The seam the tests replace: a test must not draw a full-screen surface.
var runPastSessions = func(cfg *config.Config, client kitty.KittyClient) error {
	return tui.RunHistory(cfg, client, conversation.Identity{})
}

var (
	pastSessionsBackend string
	pastSessionsView    View
)

// PastSessionsCommand returns the `past-sessions` command so the root command
// can register it. Root assembly is the only consumer.
func PastSessionsCommand() *cobra.Command { return pastSessionsCmd }

var pastSessionsCmd = &cobra.Command{
	Use:   "past-sessions",
	Short: "Open Past Sessions directly, without the dashboard",
	Long: `Open Past Sessions over every profile's conversations, as the dashboard's S
does, but with no dashboard behind it: leaving Past Sessions quits. Continue,
resume, and fork work as they do there.

It is bound to ctrl+shift+y in Kitty and in a terminal attached to a tmux
session (a popup there). In the desktop workspace, ⌘Y opens native
Conversation History; Handoff / More opens these terminal actions.`,
	Example: `  amx past-sessions
  amx past-sessions --backend tmux`,
	Args: cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		cfg, err := loadRunnableConfig()
		if err != nil {
			return err
		}
		client, err := selectTerminalsForConfig(pastSessionsBackend, cfg, newKittyClient)
		if err != nil {
			return err
		}
		if client == nil {
			// Outside Kitty a popup over an attached terminal shows what it
			// continues by switching that terminal (popup.go).
			if _, err := terminalView(pastSessionsView); err != nil {
				return err
			}
		}
		return runPastSessions(cfg, client)
	},
}

func init() {
	pastSessionsCmd.Flags().StringVar(&pastSessionsBackend, cli.BackendFlag, "", BackendFlagUsage)
	AddViewFlags(pastSessionsCmd, &pastSessionsView, "")
}
