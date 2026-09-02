package daemon

import (
	"errors"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// aslReadDeadline and aslActionDeadline bound the client half of an ASL
// exchange. Each leaves room for the daemon's own bridge timeout plus IPC, so
// a client gives up only after the daemon has already given up — never before,
// which would turn a settled mutation into an unknown one.
var (
	aslReadDeadline   = aslbridge.ReadTimeout + time.Second
	aslActionDeadline = aslbridge.ActionTimeout + time.Second
)

// ASLSnapshot returns the daemon's current ASL projection with its provenance
// and the bounded receipt window.
func (c *Client) ASLSnapshot() (aslbridge.Snapshot, aslbridge.SnapshotMeta, []aslbridge.Operation, aslbridge.Status, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeASLSnapshot}, aslReadDeadline)
	result, err := aslResult(resp, err)
	if err != nil {
		return aslbridge.Snapshot{}, aslbridge.SnapshotMeta{}, nil, aslStatusOf(resp), err
	}
	var snapshot aslbridge.Snapshot
	if result.Snapshot != nil {
		snapshot = *result.Snapshot
	}
	var meta aslbridge.SnapshotMeta
	if result.Meta != nil {
		meta = *result.Meta
	}
	return snapshot, meta, result.Receipts, result.Status, nil
}

// ASLQuestionBody fetches one question body. The result is handed to the
// caller and never cached by the client.
func (c *Client) ASLQuestionBody(req aslbridge.QuestionRequest) (aslbridge.QuestionBody, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeASLQuestion, ASL: &protocol.ASLRequest{
		PlanDir:    req.PlanDir,
		QuestionID: req.QuestionID,
	}}, aslReadDeadline)
	result, err := aslResult(resp, err)
	if err != nil {
		return aslbridge.QuestionBody{}, err
	}
	if result.Question == nil {
		return aslbridge.QuestionBody{}, errors.New("daemon returned no question body")
	}
	return *result.Question, nil
}

// ASLAct submits one explicit mutation. It is sent exactly once: an ambiguous
// timeout is reported as such rather than resent.
func (c *Client) ASLAct(req aslbridge.ActionRequest) (aslbridge.Operation, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeASLAction, ASL: &protocol.ASLRequest{
		Method:      req.Method,
		PlanDir:     req.PlanDir,
		RunID:       req.RunID,
		QuestionID:  req.QuestionID,
		OperationID: req.OperationID,
		ActionToken: req.ActionToken,
		Resolution:  req.Resolution,
		Note:        req.Note,
		Text:        req.Text,
	}}, aslActionDeadline)
	result, err := aslResult(resp, err)
	if err != nil {
		return aslbridge.Operation{}, err
	}
	if result.Operation == nil {
		return aslbridge.Operation{}, errors.New("daemon returned no operation receipt")
	}
	return *result.Operation, nil
}

// ASLWorkbench asks for a deep link into one plan's detail view.
func (c *Client) ASLWorkbench(req aslbridge.WorkbenchRequest) (aslbridge.WorkbenchLink, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeASLWorkbench, ASL: &protocol.ASLRequest{
		PlanDir: req.PlanDir,
		Focus:   req.Focus,
	}}, aslActionDeadline)
	result, err := aslResult(resp, err)
	if err != nil {
		return aslbridge.WorkbenchLink{}, err
	}
	if result.Workbench == nil {
		return aslbridge.WorkbenchLink{}, errors.New("daemon returned no workbench link")
	}
	return *result.Workbench, nil
}

func aslResult(resp protocol.Response, err error) (protocol.ASLResponse, error) {
	if err != nil {
		return protocol.ASLResponse{}, err
	}
	if !resp.OK {
		if resp.Error == "" {
			return protocol.ASLResponse{}, errors.New("daemon refused the ASL request")
		}
		return protocol.ASLResponse{}, errors.New(resp.Error)
	}
	if resp.ASL == nil {
		return protocol.ASLResponse{}, errors.New("daemon returned no ASL result")
	}
	return *resp.ASL, nil
}

func aslStatusOf(resp protocol.Response) aslbridge.Status {
	if resp.ASL == nil {
		return aslbridge.Status{}
	}
	return resp.ASL.Status
}
