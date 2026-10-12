package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// sendRun runs amx with args and standard input and returns what it wrote to
// standard output. It is a seam so tests never start a process.
var sendRun = func(bin string, args []string, stdin string) ([]byte, error) {
	// #nosec G204 -- this binary, with a validated runtime-session ID and key names.
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}

// sendFor builds what a surface sends to a tmux session with: `amx send`,
// for the reason a registered session is attached through `amx attach`. The
// command is where a pane is decided to be writable, and a surface with its
// own copy of that decision would be a second answer to it.
//
// The text goes to the command on standard input, so it is in no argument
// list here either.
func sendFor(server backend.TargetLauncher) func(session.RunningSession, commands.SendRequest) (commands.SendReport, error) {
	if server == nil {
		return nil
	}
	return func(rs session.RunningSession, req commands.SendRequest) (commands.SendReport, error) {
		if rs.RuntimeSessionID == "" {
			return commands.SendReport{}, errors.New("this session has no runtime session to send to")
		}
		bin, err := attachBinary()
		if err != nil {
			return commands.SendReport{}, err
		}
		args := []string{"send", string(rs.RuntimeSessionID), "--json"}
		for _, key := range req.Keys {
			args = append(args, "--key", key)
		}
		if len(req.Keys) == 0 {
			args = append(args, "--stdin")
		}
		if req.Confirmed {
			args = append(args, "--yes")
		}
		out, runErr := sendRun(bin, args, req.Text)
		return sendAnswer(out, runErr)
	}
}

// sendAnswer reads what `amx send --json` printed. A data envelope is input
// that was written, whatever the exit status: the command exits nonzero
// beside one when only the record of the send failed. An error envelope is a
// refusal by code. Anything else is the command not having run.
func sendAnswer(out []byte, runErr error) (commands.SendReport, error) {
	var envelope struct {
		Data *struct {
			Delivery string `json:"delivery"`
			Screen   string `json:"screen"`
		} `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &envelope); err == nil {
		switch {
		case envelope.Data != nil && envelope.Data.Delivery != "":
			return commands.SendReport{Delivery: envelope.Data.Delivery, Screen: envelope.Data.Screen}, nil
		case envelope.Error != nil && envelope.Error.Code != "":
			return commands.SendReport{}, &commands.SendRefusal{Code: envelope.Error.Code, Message: envelope.Error.Message}
		}
	}
	if runErr != nil {
		return commands.SendReport{}, fmt.Errorf("amx send: %w", runErr)
	}
	return commands.SendReport{}, errors.New("amx send answered with nothing that can be read")
}
