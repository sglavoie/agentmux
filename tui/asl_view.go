package tui

// This file is the seam between the root Bubble Tea model and the tui/asl leaf.
// It projects the model onto the leaf's input value and re-exposes the leaf's
// verdicts, geometry, and renderers under the names the rest of the root
// package and its tests already use. Everything here is projection; deciding
// what an action DOES to the model stays in model_asl_runs.go,
// model_asl_response.go, and model_asl_control.go.

import (
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/asl"
)

// aslFacts projects only what an availability verdict rests on. It deliberately
// carries no geometry: the footer palette asks whether an action is allowed
// while the root is still measuring its own footer, so charging the body height
// here would make the palette depend on itself.
func aslFacts(m Model) asl.RunsInput {
	return asl.RunsInput{
		Rows:     m.aslRows,
		Loaded:   m.aslLoaded,
		Loading:  m.aslLoading,
		Err:      m.aslErr,
		Meta:     m.aslMeta,
		Status:   m.aslStatus,
		Sessions: m.sessions,
		Now:      time.Now(),
	}
}

// aslRunsInput adds the cursor, the notices, and the geometry the renderer
// needs, charging the root's own chrome against the body height before handing
// it to the leaf, which knows nothing about the header, footer, or status bar.
func aslRunsInput(m Model) asl.RunsInput {
	in := aslFacts(m)
	in.Cursor = m.aslCursor
	in.Offset = m.aslOffset
	in.Width = m.width
	in.Height = m.height
	in.BodyHeight = rootBodyHeight(m)
	in.Notice = m.aslNotice
	in.Operations = m.aslOperations
	return in
}

func aslView(m Model) asl.ViewState { return aslFacts(m).View() }

func aslVisibleHeight(m Model) int { return aslRunsInput(m).VisibleHeight() }

func aslBannerText(m Model) string { return aslFacts(m).BannerText() }

func renderASLRuns(m Model) string { return asl.RenderRuns(aslRunsInput(m)) }

func renderASLResponseBox(m Model) string {
	return asl.RenderResponseBox(m.aslResponse, m.width)
}

func aslActionAllowed(m Model, row aslbridge.Row, method string) (bool, string) {
	return aslFacts(m).ActionAllowed(row, method)
}

func aslMarkerFor(m Model, s session.RunningSession) (string, bool) {
	return aslFacts(m).MarkerFor(s)
}
