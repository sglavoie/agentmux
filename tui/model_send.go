package tui

import (
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sendform"
)

// The send form (ModeSend): answering the prompt a tmux session is showing,
// or handing it its next prompt, from the dashboard. tui/sendform owns what
// the form holds and how it is drawn; this file owns the flow, which is short
// because every decision about whether a pane may be written to is made by
// `amx send` behind commands.Send.

// sendScreenSettle is how long an agent that was just written to is given to
// redraw before its screen is read again.
const sendScreenSettle = 600 * time.Millisecond

// openSendForm raises the form over the list for one captured session. A row
// that is asking something opens on the keys half, since a key is what such a
// row is waiting for; any other row opens on the text.
func (m Model) openSendForm(rs session.RunningSession) (Model, tea.Cmd) {
	m.send = sendform.State{
		Active:    true,
		Target:    rs,
		Title:     dashboard.SessionDisplayTitle(rs),
		FocusKeys: rs.State == "key" || rs.State == "question",
		Seq:       m.send.Seq + 1,
	}
	m.mode = ModeSend
	return m, commands.SendScreen(rs, m.send.Seq, 0)
}

// closeSendForm drops the form and everything in it. Seq survives, so an
// answer still in flight to the closed form cannot match the next one.
func (m *Model) closeSendForm() {
	m.send = sendform.State{Seq: m.send.Seq}
	m.mode = ModeList
}

func (m Model) handleSendKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.send.Active {
		m.mode = ModeList
		return m, nil
	}
	key := msg.String()
	switch key {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.closeSendForm()
		return m, nil
	}
	if m.send.InFlight {
		// One send at a time: a key pressed while an answer is on its way was
		// aimed at a screen that is about to change.
		return m, nil
	}
	switch key {
	case keyTab, keyShiftTab:
		m.send.FocusKeys = !m.send.FocusKeys
		return m, nil
	case keyCtrlS:
		text := strings.TrimRight(m.send.Draft, "\n")
		if strings.TrimSpace(text) == "" {
			m.send.Notice, m.send.Warn = "There is no text to send.", true
			return m, nil
		}
		return m.submitSend(commands.SendRequest{Text: text})
	}
	if m.send.FocusKeys {
		if name, ok := sendform.KeyFor(key); ok {
			return m.submitSend(commands.SendRequest{Keys: []string{name}})
		}
		return m, nil
	}
	switch key {
	case keyEnter:
		m.send.Draft += "\n"
	case keyBackspace:
		if runes := []rune(m.send.Draft); len(runes) > 0 {
			m.send.Draft = string(runes[:len(runes)-1])
		}
	default:
		m.send.Draft += msg.Key().Text
	}
	return m, nil
}

func (m Model) submitSend(req commands.SendRequest) (tea.Model, tea.Cmd) {
	req.Confirmed = m.send.Confirmed
	m.send.Seq++
	m.send.InFlight = true
	m.send.Notice, m.send.Warn = "", false
	return m, commands.Send(m.send.Target, req, m.send.Seq)
}

// handleSendDone shows what a send came to. Text that was written closes the
// form: the prompt is the agent's now and the list is where its progress
// shows. A key that was written keeps the form open on the screen it led to.
func (m Model) handleSendDone(msg commands.SendDoneMsg) (tea.Model, tea.Cmd) {
	if !m.send.Active || msg.Seq != m.send.Seq {
		return m, nil
	}
	m.send.InFlight = false
	var refusal *commands.SendRefusal
	switch {
	case errors.As(msg.Err, &refusal) && refusal.Code == commands.SendRefusedConfirmation:
		m.send.Confirmed = true
		m.send.Notice, m.send.Warn = "This session runs an agent-session-loop run, and what is sent reaches its supervisor. Send again to confirm.", true
		return m, nil
	case msg.Err != nil:
		m.send.Notice, m.send.Warn = sendRefusalText(msg.Err), true
		return m, commands.SendScreen(m.send.Target, m.send.Seq, 0)
	}
	m.send.Confirmed = false
	if !msg.Keys {
		title := m.send.Title
		m.closeSendForm()
		return m, tea.Batch(m.pushToast(Toast{Severity: toastSuccess, Text: "sent to " + title}), m.requestTopologyRefresh())
	}
	m.send.Notice = "Sent."
	return m, commands.SendScreen(m.send.Target, m.send.Seq, sendScreenSettle)
}

func (m Model) handleSendScreen(msg commands.SendScreenMsg) (tea.Model, tea.Cmd) {
	if !m.send.Active || msg.Seq != m.send.Seq {
		return m, nil
	}
	m.send.ScreenLoaded, m.send.Screen, m.send.ScreenErr = true, msg.Text, ""
	if msg.Err != nil {
		m.send.Screen, m.send.ScreenErr = "", msg.Err.Error()
	}
	return m, nil
}

func renderSendBox(m Model) string { return sendform.Render(m.send, m.width) }

// sendRefusalText says what a refused send means in the form. The command's
// own message names its flags, which are not what an operator in this form
// can reach for; the three refusals the form can do something about are
// worded for it, and every other one is the command's sentence.
func sendRefusalText(err error) string {
	var refusal *commands.SendRefusal
	if errors.As(err, &refusal) {
		switch refusal.Code {
		case "prompt_showing":
			return "The agent is asking something, and text typed there would be read as its answer. Answer it from the keys half (tab)."
		case "no_prompt_showing":
			return "The agent is not asking anything, so a key would be typed into its prompt box."
		case "screen_unreadable":
			return "The agent's screen could not be read, so there is no telling what this would land on. Open the session to type there."
		}
	}
	return sentenceCase(err.Error())
}
