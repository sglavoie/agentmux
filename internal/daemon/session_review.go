package daemon

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

type reviewEntry struct {
	Epoch     string `json:"epoch,omitempty"`
	Accounted bool   `json:"accounted,omitempty"`
	Reviewed  bool   `json:"reviewed,omitempty"`
	Note      string `json:"note,omitempty"`
	Revision  string `json:"revision,omitempty"`
}
type reviewFile struct {
	Version  int                    `json:"version"`
	Sessions map[string]reviewEntry `json:"sessions"`
	Projects map[string]string      `json:"projects"`
}
type reviewUndo struct {
	Before                         map[string]reviewEntry
	After                          map[string]reviewEntry
	Project, BeforeNote, AfterNote string
}

// All access, including the infrequent atomic annotation writes, is serialized
// by stateMu. Live hook updates never write this file. A failed load blocks edits
// so user-authored notes can never be silently replaced with an empty store.
type reviewStore struct {
	path    string
	loadErr error
	data    reviewFile
	undo    map[string]reviewUndo
	order   []string
}

func (h *Handler) loadSessionReviews() {
	h.reviews.path = filepath.Join(filepath.Dir(h.runtimeCheckpointPath), "session-reviews.json")
	data, err := os.ReadFile(h.reviews.path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		var loaded reviewFile
		err = json.Unmarshal(data, &loaded)
		if err == nil && loaded.Version != 1 {
			err = errors.New("unsupported session reviews version")
		}
		if err == nil {
			for _, entry := range loaded.Sessions {
				if !validReviewNote(entry.Note) {
					err = errors.New("invalid saved session note")
					break
				}
			}
			for _, note := range loaded.Projects {
				if !validReviewNote(note) {
					err = errors.New("invalid saved project note")
					break
				}
			}
		}
		if err == nil {
			h.reviews.data = loaded
		}
	}
	h.reviews.loadErr = err
	if err != nil {
		slog.Error("session notes unavailable; existing file preserved", "error", err)
	}
}

func (h *Handler) sessionReviewLocked(slot int, st *state.WindowState) protocol.SessionReview {
	id := h.slotRuntimeLocked(slot)
	entry := h.reviews.data.Sessions[id]
	return protocol.SessionReview{Epoch: st.ReviewEpoch, Accounted: entry.Accounted && entry.Epoch == st.ReviewEpoch,
		Reviewed: entry.Reviewed, Note: entry.Note, ProjectNote: h.reviews.data.Projects[h.runtimeLabels[id].project]}
}

func reviewAttentionIcon(icon string) bool {
	switch icon {
	case "stop", "key", "question", "warning", "error":
		return true
	}
	return false
}

// Observe completions even when a live ASL lease masks the agent's icon.
func observeReviewTurn(st *state.WindowState) {
	turn := st.Reducer.AgentTurn(time.Now().Unix())
	if turn == nil {
		return
	}
	signature := fmt.Sprintf("%s:%d", turn.IconState, turn.FinishedAt)
	if signature != st.ReviewTurn && reviewAttentionIcon(turn.IconState) {
		advanceReviewEpoch(st)
	}
	st.ReviewTurn = signature
}

func validReviewNote(note string) bool {
	return utf8.ValidString(note) && utf8.RuneCountInString(note) <= 500 && !strings.ContainsFunc(note, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' })
}

// Random epochs cannot collide with a generation from an older checkpoint.
func advanceReviewEpoch(st *state.WindowState) { st.ReviewEpoch = rand.Text() }

func (h *Handler) handleSessionReview(req protocol.Request) protocol.Response {
	fail := func(err error) protocol.Response { return protocol.Response{Error: "session review: " + err.Error()} }
	r := req.Review
	if r == nil {
		return fail(errors.New("missing review request"))
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.reviews.loadErr != nil {
		return fail(h.reviews.loadErr)
	}
	data := reviewFile{Version: 1, Sessions: maps.Clone(h.reviews.data.Sessions), Projects: maps.Clone(h.reviews.data.Projects)}
	if data.Sessions == nil {
		data.Sessions = map[string]reviewEntry{}
	}
	if data.Projects == nil {
		data.Projects = map[string]string{}
	}
	token := rand.Text()
	undo := reviewUndo{Before: map[string]reviewEntry{}, After: map[string]reviewEntry{}}
	if r.UndoToken != "" {
		if len(r.Targets) != 0 || r.Accounted != nil || r.Note != nil || r.Project != "" {
			return fail(errors.New("undo cannot include edits"))
		}
		saved, ok := h.reviews.undo[r.UndoToken]
		if !ok {
			return fail(errors.New("undo expired"))
		}
		for id, after := range saved.After {
			if data.Sessions[id] != after {
				return fail(errors.New("annotations changed since this action; undo refused"))
			}
			data.Sessions[id] = saved.Before[id]
		}
		if saved.Project != "" {
			if data.Projects[saved.Project] != saved.AfterNote {
				return fail(errors.New("project note changed since this action; undo refused"))
			}
			data.Projects[saved.Project] = saved.BeforeNote
		}
		undo = saved
	} else {
		if len(r.Targets) == 0 || len(r.Targets) > 1000 || (r.Accounted == nil && r.Note == nil) {
			return fail(errors.New("select sessions and an annotation to change"))
		}
		if r.Project == "" && len(r.Targets) != 1 && r.Note != nil {
			return fail(errors.New("a shared note requires a project"))
		}
		if r.Note != nil {
			if !validReviewNote(*r.Note) {
				return fail(errors.New("notes must be one line, at most 500 characters, without control characters"))
			}
		}
		for id, epoch := range r.Targets {
			slot, ok := h.runtimeRoutes.byRuntime[id]
			if !ok {
				return fail(fmt.Errorf("session %s is no longer available", id))
			}
			st, ok := h.windowStateLocked(slot)
			if !ok {
				return fail(fmt.Errorf("session %s has no current state", id))
			}
			if r.Project != "" && h.runtimeLabels[id].project != r.Project {
				return fail(errors.New("project membership changed; refresh and retry"))
			}
			if r.Accounted != nil && *r.Accounted && st.ReviewEpoch != epoch {
				return fail(errors.New("new activity arrived; review it before marking accounted for"))
			}
			before := data.Sessions[id]
			after := before
			after.Revision = token
			if r.Accounted != nil {
				after.Accounted, after.Epoch, after.Reviewed = *r.Accounted, st.ReviewEpoch, *r.Accounted
			}
			if r.Note != nil && r.Project == "" {
				after.Note = strings.TrimSpace(*r.Note)
			}
			undo.Before[id], undo.After[id], data.Sessions[id] = before, after, after
		}
		if r.Project != "" && r.Note != nil {
			undo.Project, undo.BeforeNote, undo.AfterNote = r.Project, data.Projects[r.Project], strings.TrimSpace(*r.Note)
			data.Projects[r.Project] = undo.AfterNote
		}
	}
	// Commit the observed epoch before promising a durable acknowledgement.
	// A crash immediately after this reply must not restore an older epoch and
	// spuriously expire a review that the operator just saved.
	if r.Accounted != nil && *r.Accounted {
		if err := h.writeRuntimeCheckpoint(h.runtimeCheckpointEntriesLocked()); err != nil {
			return fail(err)
		}
	}
	if h.reviews.path != "" {
		encoded, err := json.Marshal(data)
		if err != nil {
			return fail(err)
		}
		if err := atomicfile.Write(h.reviews.path, encoded, 0600, atomicfile.ParentMode(0700), atomicfile.RequireRegularTarget()); err != nil {
			return fail(err)
		}
	}
	h.reviews.data = data
	if r.UndoToken != "" {
		delete(h.reviews.undo, r.UndoToken)
		token = ""
	} else {
		if h.reviews.undo == nil {
			h.reviews.undo = map[string]reviewUndo{}
		}
		h.reviews.undo[token] = undo
		h.reviews.order = append(h.reviews.order, token)
		if len(h.reviews.order) > 32 {
			delete(h.reviews.undo, h.reviews.order[0])
			h.reviews.order = h.reviews.order[1:]
		}
	}
	h.companionDirty()
	return protocol.Response{OK: true, Review: &protocol.SessionReviewResult{UndoToken: token, Count: len(undo.Before)}}
}
