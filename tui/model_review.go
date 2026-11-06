package tui

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/sendform"
)

type reviewEditor struct {
	Request protocol.SessionReviewRequest
	Input   sendform.State
	Error   string
}
type reviewDoneMsg struct {
	Result *protocol.SessionReviewResult
	Err    error
}

// Project commands operate on the captured visible project's sessions. Family
// summaries may encompass several configured projects; acknowledge all members,
// but shared notes require one unambiguous configured project.
func (m Model) collectReviewTargets(project bool) ([]session.RunningSession, string) {
	s, ok := m.selectedSession()
	if !project {
		if ok {
			return []session.RunningSession{s}, ""
		}
		return nil, ""
	}
	var rows []session.RunningSession
	if ok {
		for _, row := range m.filtered {
			if row.Project == s.Project {
				rows = append(rows, row)
			}
		}
	} else if item, found := m.selectedItem(); found && item.Kind == dashboard.ItemFamilySummary {
		for _, index := range item.Members {
			if index >= 0 && index < len(m.filtered) {
				rows = append(rows, m.filtered[index])
			}
		}
	}
	name := ""
	if len(rows) > 0 {
		name = rows[0].Project
		for _, row := range rows {
			if row.Project != name {
				name = ""
				break
			}
		}
	}
	return rows, name
}

func reviewTargets(ctx commands.Context, project bool) ([]session.RunningSession, string) {
	if project {
		return ctx.ReviewRows, ctx.ReviewProject
	}
	if ctx.HasTarget {
		return []session.RunningSession{ctx.Target}, ""
	}
	return nil, ""
}

func reviewRequest(rows []session.RunningSession, project string) protocol.SessionReviewRequest {
	r := protocol.SessionReviewRequest{Targets: map[string]string{}, Project: project}
	for _, s := range rows {
		if s.RuntimeSessionID != "" {
			r.Targets[string(s.RuntimeSessionID)] = s.Review.Epoch
		}
	}
	return r
}

func (m Model) submitReview(r protocol.SessionReviewRequest) (Model, tea.Cmd) {
	if m.reviewBusy {
		return m, nil
	}
	m.reviewBusy = true
	request := m.actionDaemon()
	return m, func() tea.Msg {
		response, err := request(protocol.Request{Type: protocol.TypeSessionReview, Review: &r})
		if err == nil && !response.OK {
			err = errors.New(response.Error)
		}
		if err == nil && response.Review == nil {
			err = errors.New("daemon did not confirm the annotation")
		}
		return reviewDoneMsg{response.Review, err}
	}
}

func (m Model) handleReviewDone(msg reviewDoneMsg) (tea.Model, tea.Cmd) {
	m.reviewBusy = false
	if msg.Err != nil {
		if m.mode == ModeReviewNote {
			m.reviewEditor.Error = msg.Err.Error()
			return m, nil
		}
		return m, m.pushToast(Toast{Severity: toastError, Text: msg.Err.Error()})
	}
	m.reviewUndo = msg.Result.UndoToken
	if m.mode == ModeReviewNote {
		m.mode = ModeList
	}
	text := "Annotation saved · Undo Annotation in : palette"
	if m.reviewUndo == "" {
		text = "Annotation undone"
	}
	return m, tea.Batch(m.pushToast(Toast{Severity: toastSuccess, Text: text}), m.requestTopologyRefresh())
}

func reviewCommandDeclarations() []commands.Command[Model] {
	var catalog []commands.Command[Model]
	for _, project := range []bool{false, true} {
		for _, note := range []bool{false, true} {
			scope := "Session"
			if project {
				scope = "Project"
			}
			verb := "Accounted"
			if note {
				verb = "Note"
			}
			catalog = append(catalog, commands.Command[Model]{
				ID: commands.ID("review." + scope + verb), Group: commands.GroupReview,
				Title: func(m Model, ctx commands.Context) string {
					if note {
						return "Edit " + scope + " Note…"
					}
					rows, _ := reviewTargets(ctx, project)
					all := len(rows) > 0
					for _, s := range rows {
						all = all && s.Review.Accounted
					}
					if all {
						return "Clear " + scope + " Acknowledgement"
					}
					return "Mark " + scope + " Accounted For"
				},
				Description: "Record your review without changing agent state. New attention resurfaces; notes persist.",
				Availability: func(m Model, ctx commands.Context) commands.Availability {
					rows, name := reviewTargets(ctx, project)
					if m.reviewBusy {
						return commands.Unavailable("Saving annotation")
					}
					if len(rows) == 0 || (note && project && name == "") {
						return commands.Unavailable("Select a session or a single project")
					}
					for _, s := range rows {
						if s.RuntimeSessionID == "" {
							return commands.Unavailable("Session is unavailable")
						}
					}
					return commands.Available()
				},
				Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
					rows, name := reviewTargets(ctx, project)
					r := reviewRequest(rows, name)
					if note {
						draft := rows[0].Review.Note
						if project {
							draft = rows[0].Review.ProjectNote
						}
						m.reviewEditor = reviewEditor{Request: r, Input: sendform.State{Draft: draft, Cursor: len(draft)}}
						m.mode = ModeReviewNote
						return m, nil
					}
					all := true
					for _, s := range rows {
						all = all && s.Review.Accounted
					}
					want := !all
					r.Accounted = &want
					return m.submitReview(r)
				},
			})
		}
	}
	return append(catalog, commands.Command[Model]{ID: "review.undo", Group: commands.GroupReview, Title: commands.StaticTitle[Model]("Undo Annotation"), Description: "Restore the previous acknowledgement and note; newer activity stays unacknowledged.",
		Availability: func(m Model, ctx commands.Context) commands.Availability {
			if m.reviewUndo == "" || m.reviewBusy {
				return commands.Unavailable("No annotation to undo")
			}
			return commands.Available()
		},
		Execute: func(m Model, ctx commands.Context) (Model, tea.Cmd) {
			return m.submitReview(protocol.SessionReviewRequest{UndoToken: m.reviewUndo})
		},
	})
}

func (m Model) handleReviewNoteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.reviewBusy {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.mode = ModeList
		return m, nil
	case "enter":
		note := m.reviewEditor.Input.Draft
		if utf8.RuneCountInString(note) > 500 {
			m.reviewEditor.Error = "Use at most 500 characters"
			return m, nil
		}
		r := m.reviewEditor.Request
		r.Note = &note
		return m.submitReview(r)
	}
	if !m.reviewEditor.Input.Edit(msg.String(), max(1, m.width-8)) {
		m.insertReviewNote(msg.Key().Text)
	}
	return m, nil
}
func (m *Model) insertReviewNote(text string) {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, text)
	m.reviewEditor.Input.Insert(text)
}
func renderReviewNoteBox(m Model) string {
	title := "Session note"
	if m.reviewEditor.Request.Project != "" {
		title = "Project note · " + m.reviewEditor.Request.Project
	}
	draft := m.reviewEditor.Input.Draft
	cursor := min(m.reviewEditor.Input.Cursor, len(draft))
	width := max(1, min(68, m.width-6))
	rows := layout.WrapHard(draft[:cursor]+"▌"+draft[cursor:], width)
	cursorLine := len(layout.WrapHard(draft[:cursor]+"▌", width)) - 1
	visible := max(1, min(8, newRootChrome(m).bodyHeight()-9))
	start := max(0, cursorLine-visible+1)
	end := min(len(rows), start+visible)
	content := layout.StyledTruncate(title, width, "…") + "\n\n" + strings.Join(rows[start:end], "\n") + "\n\n" + layout.StyledTruncate("Enter save · Esc cancel", width, "…")
	if m.reviewBusy {
		content += "\nSaving…"
	}
	if m.reviewEditor.Error != "" {
		content += "\n" + layout.StyledTruncate(m.reviewEditor.Error, width, "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(content)
}
