package aslbridge

// This file is every request the daemon issues on a caller's behalf: the two
// reads that must not be cached (a question body, a tokenized Workbench link)
// and the one mutation entry point. Validation happens before any round trip,
// and a mutation additionally demands a connection the daemon may currently act
// on, which is what mutableConn distinguishes from liveConn.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// QuestionBody fetches the prose for one question. The body is never cached,
// logged, or exported: it is returned to exactly one caller and forgotten.
func (m *Manager) QuestionBody(ctx context.Context, req QuestionRequest) (QuestionBody, error) {
	if strings.TrimSpace(req.PlanDir) == "" {
		return QuestionBody{}, fmt.Errorf("%w: plan_dir is required", ErrInvalidRequest)
	}
	c, err := m.liveConn()
	if err != nil {
		return QuestionBody{}, err
	}
	params := map[string]any{"plan_dir": req.PlanDir}
	if req.QuestionID != "" {
		params["question_id"] = req.QuestionID
	}
	callCtx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	raw, err := c.call(callCtx, MethodQuestionGet, params)
	if err != nil {
		return QuestionBody{}, err
	}
	var body QuestionBody
	if err := json.Unmarshal(raw, &body); err != nil {
		m.failSession(c, contaminationError("question.get result"))
		return QuestionBody{}, ErrUnavailable
	}
	return body, nil
}

// Workbench returns a tokenized deep link for one plan.
func (m *Manager) Workbench(ctx context.Context, req WorkbenchRequest) (WorkbenchLink, error) {
	if strings.TrimSpace(req.PlanDir) == "" {
		return WorkbenchLink{}, fmt.Errorf("%w: plan_dir is required", ErrInvalidRequest)
	}
	if req.Focus != "" && !validFocus(req.Focus) {
		return WorkbenchLink{}, fmt.Errorf("%w: unsupported workbench focus %q", ErrInvalidRequest, req.Focus)
	}
	c, err := m.liveConn()
	if err != nil {
		return WorkbenchLink{}, err
	}
	params := map[string]any{"plan_dir": req.PlanDir}
	if req.Focus != "" {
		params["focus"] = req.Focus
	}
	callCtx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	raw, err := c.call(callCtx, MethodWorkbenchEnsure, params)
	if err != nil {
		return WorkbenchLink{}, err
	}
	var link WorkbenchLink
	if err := json.Unmarshal(raw, &link); err != nil {
		m.failSession(c, contaminationError("workbench.ensure result"))
		return WorkbenchLink{}, ErrUnavailable
	}
	return link, nil
}

// ----------------------------------------------------------------
// Mutations

// Act performs one explicit mutation and returns its receipt. It is never
// retried: an ambiguous action is reported to the caller as ambiguous rather
// than resubmitted, because a second attempt can spawn a second run.
func (m *Manager) Act(ctx context.Context, req ActionRequest) (Operation, error) {
	params, err := actionParams(req)
	if err != nil {
		return Operation{}, err
	}
	c, err := m.mutableConn()
	if err != nil {
		return Operation{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, ActionTimeout)
	defer cancel()
	raw, err := c.call(callCtx, req.Method, params)
	if err != nil {
		m.classifyActionFailure(err)
		return Operation{}, err
	}
	var op Operation
	if err := json.Unmarshal(raw, &op); err != nil {
		// ASL answered and the daemon cannot read the answer, so whether the
		// mutation landed is unknown. That is the ambiguous case, not a refusal.
		m.revokeMutations(reasonAmbiguousOperation)
		m.failSession(c, contaminationError(req.Method+" result"))
		return Operation{}, ErrUnavailable
	}
	m.recordReceipt(op)
	return op, nil
}

// classifyActionFailure decides what a failed mutation costs the grant.
//
// A typed refusal is a definite outcome: ASL decided, wrote nothing, and said
// why. Anything else — a timeout, a lost child, a frame the daemon could not
// correlate — leaves the daemon unable to say whether ASL applied the request.
// The mutation is never retried either way, so the safe response to not knowing
// is to stop acting until a fresh snapshot and handshake re-establish what is
// true, rather than to let an operator issue a second action against state the
// daemon can no longer vouch for.
func (m *Manager) classifyActionFailure(err error) {
	var refusal *Error
	if !errors.As(err, &refusal) {
		m.revokeMutations(reasonAmbiguousOperation)
		m.requestRefresh()
		return
	}
	switch refusal.Code {
	case CodeStaleSnapshot, CodeContractSetMismatch:
		// The bridge just told the daemon its grant is no longer good. Believe it
		// rather than waiting for the next poll to notice.
		m.revokeMutations(reasonHandshakeStale)
		m.requestRefresh()
	}
}

func actionParams(req ActionRequest) (map[string]any, error) {
	if strings.TrimSpace(req.PlanDir) == "" {
		return nil, fmt.Errorf("%w: plan_dir is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.OperationID) == "" {
		return nil, fmt.Errorf("%w: operation_id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.ActionToken) == "" {
		return nil, fmt.Errorf("%w: action_token is required", ErrInvalidRequest)
	}
	params := map[string]any{
		"plan_dir":     req.PlanDir,
		"operation_id": req.OperationID,
		"action_token": req.ActionToken,
	}
	if req.RunID != "" {
		params["run_id"] = req.RunID
	}
	if req.QuestionID != "" {
		params["question_id"] = req.QuestionID
	}
	switch req.Method {
	case MethodQuestionReply, MethodRunResume:
		if strings.TrimSpace(req.Text.Reveal()) == "" {
			return nil, fmt.Errorf("%w: text is required for %s", ErrInvalidRequest, req.Method)
		}
		params["text"] = req.Text.Reveal()
	case MethodQuestionResolve:
		if strings.TrimSpace(req.Resolution) == "" {
			return nil, fmt.Errorf("%w: action is required for %s", ErrInvalidRequest, req.Method)
		}
		params["action"] = req.Resolution
		if req.Note != "" {
			params["note"] = req.Note
		}
	case MethodRunStopAfterCurrent, MethodRunAbort:
	default:
		return nil, fmt.Errorf("%w: unsupported method %q", ErrInvalidRequest, req.Method)
	}
	return params, nil
}

func (m *Manager) liveConn() (*conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mode == ModeDisabled {
		return nil, ErrDisabled
	}
	if !m.connected || m.conn == nil {
		return nil, ErrUnavailable
	}
	return m.conn, nil
}

func (m *Manager) mutableConn() (*conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mode == ModeDisabled {
		return nil, ErrDisabled
	}
	if !m.connected || m.conn == nil {
		return nil, ErrUnavailable
	}
	if !m.mutationsEnabledLocked() {
		return nil, ErrMutationsDisabled
	}
	return m.conn, nil
}

func validFocus(focus string) bool {
	switch focus {
	case FocusReply, FocusVerification, FocusActive, FocusEvents:
		return true
	}
	return false
}
