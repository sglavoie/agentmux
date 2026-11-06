package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestReviewCommandCapturesEpochAndKeepsNoteOnlyEditsSeparate(t *testing.T) {
	original := reviewSend
	t.Cleanup(func() { reviewSend = original })
	id := "rs-11111111111111111111111111111111"
	for _, args := range [][]string{{"--accounted"}, {"--note", ""}, {"--clear"}} {
		var requests []protocol.Request
		reviewSend = func(r protocol.Request) (protocol.Response, error) {
			requests = append(requests, r)
			if r.Type == protocol.TypeStateQuery {
				return protocol.Response{OK: true, RuntimeSlots: map[int]protocol.RuntimeSlot{1: {RuntimeSessionID: id}}, States: map[int]protocol.WindowStateSnapshot{1: {Review: protocol.SessionReview{Epoch: "current"}}}}, nil
			}
			return protocol.Response{OK: true, Review: &protocol.SessionReviewResult{UndoToken: "receipt", Count: 1}}, nil
		}
		command := newReviewCommand()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs(append([]string{"--runtime-session", id, "--json"}, args...))
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		if len(requests) != 2 || requests[1].Review.Targets[id] != "current" {
			t.Fatalf("requests: %+v", requests)
		}
		review := requests[1].Review
		if args[0] == "--note" && (review.Accounted != nil || review.Note == nil || *review.Note != "") {
			t.Fatalf("note changed acknowledgement: %+v", review)
		}
		if !strings.Contains(output.String(), `"undo_token":"receipt"`) {
			t.Fatal(output.String())
		}
	}
}

func TestReviewCommandRefusalsAndUndo(t *testing.T) {
	original := reviewSend
	t.Cleanup(func() { reviewSend = original })
	for _, args := range [][]string{{}, {"--accounted", "--clear"}, {"--undo", "x", "--note", "note"}, {"--runtime-session", "invalid", "--accounted"}} {
		reviewSend = func(protocol.Request) (protocol.Response, error) {
			t.Fatal("invalid invocation reached daemon")
			return protocol.Response{}, nil
		}
		command := newReviewCommand()
		command.SilenceUsage = true
		command.SilenceErrors = true
		command.SetArgs(args)
		if err := command.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	reviewSend = func(r protocol.Request) (protocol.Response, error) {
		if r.Review == nil || r.Review.UndoToken != "receipt" || len(r.Review.Targets) != 0 {
			t.Fatalf("undo: %+v", r)
		}
		return protocol.Response{}, errors.New("unavailable")
	}
	command := newReviewCommand()
	command.SilenceUsage = true
	command.SilenceErrors = true
	command.SetArgs([]string{"--undo", "receipt"})
	if err := command.Execute(); err == nil {
		t.Fatal("transport failure reported success")
	}
}
