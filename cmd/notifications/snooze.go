package notifications

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// snoozeClient is the slice of the daemon the snooze surfaces need. Both the
// snooze leaf and the test leaf's pre-check take it, so tests swap one seam.
type snoozeClient interface {
	NotificationSnooze(protocol.SnoozeRequest) (protocol.SnoozeState, error)
}

var (
	notificationsSnoozeClient = func() snoozeClient { return ipc.NewClient() }
	notificationsNow          = time.Now
)

var snoozeJSON bool

var notificationsSnoozeCmd = &cobra.Command{
	Use:   "snooze [DURATION | until HH:MM | indefinite | off]",
	Short: "Silence every notification for a while, or until turned off",
	Long: `Snoozes every Agent Mux notification in the running daemon.

  amx notifications snooze               show the snooze in force
  amx notifications snooze 1h            for a duration (Go syntax: 30m, 1h30m)
  amx notifications snooze until 08:30   until the next 08:30, local time
  amx notifications snooze indefinite    until turned off
  amx notifications snooze off           restore delivery

The snooze lives in the daemon's memory only: it never rewrites config.toml,
and restarting the daemon forgets it. Quiet hours are configuration and are
unaffected. The daemon is not started on your behalf, because a snooze in a
daemon that was not running would change nothing.

With --json it reports {snoozed, indefinite, snoozed_until, until}.`,
	Example: `  amx notifications snooze 1h
  amx notifications snooze until 08:30
  amx notifications snooze off --json`,
	Args:              cobra.RangeArgs(0, 2),
	ValidArgsFunction: completeSnoozeArgs,
	RunE:              runNotificationsSnooze,
}

// completeSnoozeArgs offers the snooze forms parseSnoozeArgs accepts: two
// common durations and the three words. After `until` comes a HH:MM time,
// which has nothing useful to suggest, and nothing comes after that.
func completeSnoozeArgs(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{"30m", "1h", "until", "indefinite", "off"}, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	notificationsSnoozeCmd.Flags().BoolVar(&snoozeJSON, "json", false, "emit schema-v1 JSON")
}

// snoozePayload is the `notifications snooze --json` data. SnoozedUntil is a
// Unix second and Until the same instant in local RFC 3339; both are omitted
// unless a timed snooze is in force.
type snoozePayload struct {
	Snoozed      bool   `json:"snoozed"`
	Indefinite   bool   `json:"indefinite"`
	SnoozedUntil int64  `json:"snoozed_until,omitempty"`
	Until        string `json:"until,omitempty"`
}

func runNotificationsSnooze(cmd *cobra.Command, args []string) error {
	// With no argument the request carries no mode, which the daemon answers
	// as a read: the snooze in force, unchanged.
	var req protocol.SnoozeRequest
	if len(args) > 0 {
		var err error
		if req, err = parseSnoozeArgs(args, notificationsNow()); err != nil {
			return cli.CommandFailure(cmd, snoozeJSON, "invalid_arguments", err)
		}
	}
	state, err := notificationsSnoozeClient().NotificationSnooze(req)
	if err != nil {
		code := "daemon_unavailable"
		if strings.HasPrefix(err.Error(), "daemon error: ") {
			code = "snooze_refused"
		}
		return cli.CommandFailure(cmd, snoozeJSON, code, fmt.Errorf("snooze notifications: %w", err))
	}
	if snoozeJSON {
		return cli.WriteJSON(cmd, snoozePayloadOf(state))
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), describeSnooze(state))
	return err
}

// parseSnoozeArgs maps the operator's words onto a snooze request. A duration
// must be positive, and `until HH:MM` names the next such local time — today
// if it is still ahead, otherwise tomorrow — so the snooze always ends in the
// future.
func parseSnoozeArgs(args []string, now time.Time) (protocol.SnoozeRequest, error) {
	if len(args) == 2 {
		if args[0] != "until" {
			return protocol.SnoozeRequest{}, fmt.Errorf("unexpected %q; use `until HH:MM`", args[0])
		}
		clock, err := time.ParseInLocation("15:04", args[1], now.Location())
		if err != nil {
			return protocol.SnoozeRequest{}, fmt.Errorf("%q is not a HH:MM time", args[1])
		}
		end := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
		if !end.After(now) {
			end = end.AddDate(0, 0, 1)
		}
		return protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: end.Unix()}, nil
	}
	switch args[0] {
	case "indefinite":
		return protocol.SnoozeRequest{Mode: protocol.SnoozeModeIndefinite}, nil
	case "off":
		return protocol.SnoozeRequest{Mode: protocol.SnoozeModeClear}, nil
	case "until":
		return protocol.SnoozeRequest{}, errors.New("`until` needs a HH:MM time")
	}
	d, err := time.ParseDuration(args[0])
	if err != nil {
		return protocol.SnoozeRequest{}, fmt.Errorf("%q is not a duration, `until HH:MM`, `indefinite`, or `off`", args[0])
	}
	if d <= 0 {
		return protocol.SnoozeRequest{}, fmt.Errorf("snooze duration %s must be positive", args[0])
	}
	return protocol.SnoozeRequest{Mode: protocol.SnoozeModeUntil, Until: now.Add(d).Unix()}, nil
}

func snoozePayloadOf(state protocol.SnoozeState) snoozePayload {
	payload := snoozePayload{Indefinite: state.Indefinite, Snoozed: state.Indefinite || state.SnoozedUntil != 0}
	if !state.Indefinite && state.SnoozedUntil != 0 {
		payload.SnoozedUntil = state.SnoozedUntil
		payload.Until = time.Unix(state.SnoozedUntil, 0).In(notificationsNow().Location()).Format(time.RFC3339)
	}
	return payload
}

// describeSnooze is the one sentence both leaves print for a snooze.
func describeSnooze(state protocol.SnoozeState) string {
	switch {
	case state.Indefinite:
		return "Notifications are snoozed until turned off."
	case state.SnoozedUntil != 0:
		until := time.Unix(state.SnoozedUntil, 0).In(notificationsNow().Location())
		return fmt.Sprintf("Notifications are snoozed until %s.", until.Format("2006-01-02 15:04"))
	default:
		return "Notifications are not snoozed."
	}
}
