package session

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

func init() { sessionCmd.AddCommand(newReviewCommand()) }

// reviewSend is injectable so command tests never contact the user's daemon.
var reviewSend = func(r protocol.Request) (protocol.Response, error) { return ipc.NewClient().Send(r) }

func newReviewCommand() *cobra.Command {
	var id, note, undo string
	var accounted, clear, asJSON bool
	cmd := &cobra.Command{Use: "review", Short: "Mark a session accounted for, save a note, or undo an annotation", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&id, "runtime-session", "", "session to annotate (defaults to $AMX_RUNTIME_SESSION_ID)")
	cmd.Flags().BoolVar(&accounted, "accounted", false, "mark accounted for until meaningful new activity")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the acknowledgement, preserving notes")
	cmd.Flags().StringVar(&note, "note", "", "save a one-line note; an empty value removes it")
	cmd.Flags().StringVar(&undo, "undo", "", "undo an annotation using its returned token")
	cli.AddJSONFlag(cmd, &asJSON)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		fail := func(err error) error { return cli.CommandFailure(cmd, asJSON, "session_review_failed", err) }
		if (accounted && clear) || (undo != "" && (accounted || clear || cmd.Flags().Changed("note") || id != "")) {
			return fail(errors.New("choose undo, or a session edit; --accounted and --clear are exclusive"))
		}
		r := protocol.SessionReviewRequest{UndoToken: undo}
		if undo == "" {
			if !accounted && !clear && !cmd.Flags().Changed("note") {
				return fail(errors.New("choose --accounted, --clear, --note, or --undo"))
			}
			if id == "" {
				id = currentRuntimeSession()
			}
			if _, err := cli.ParseRuntimeSessionID(id); err != nil {
				return fail(err)
			}
			snapshot, err := reviewSend(protocol.Request{Type: protocol.TypeStateQuery})
			if err != nil {
				return fail(err)
			}
			if !snapshot.OK {
				return fail(errors.New(snapshot.Error))
			}
			r.Targets = map[string]string{}
			for slot, runtime := range snapshot.RuntimeSlots {
				if runtime.RuntimeSessionID == id {
					r.Targets[id] = snapshot.States[slot].Review.Epoch
				}
			}
			if len(r.Targets) == 0 {
				return fail(errors.New("session is not available in the daemon"))
			}
			if accounted || clear {
				r.Accounted = &accounted
			}
			if cmd.Flags().Changed("note") {
				r.Note = &note
			}
		}
		response, err := reviewSend(protocol.Request{Type: protocol.TypeSessionReview, Review: &r})
		if err != nil {
			return fail(err)
		}
		if !response.OK {
			return fail(errors.New(response.Error))
		}
		if response.Review == nil {
			return fail(errors.New("daemon did not confirm the annotation"))
		}
		if asJSON {
			return cli.WriteJSON(cmd, response.Review)
		}
		if response.Review.UndoToken == "" {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Annotation undone")
		} else {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Annotation saved. Undo: amx session review --undo %s\n", response.Review.UndoToken)
		}
		return err
	}
	return cmd
}
