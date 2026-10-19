package debugger

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const ProbeSchemaVersion = 1

const ProbeScope = "This validates AgentMux client-to-daemon delivery and routing without changing reducer, renderer, or notification state; it does not prove that a harness invoked its hook."

type ProbeClient interface {
	SendWithAutoStartObserved(protocol.Request, string, ipc.DeliveryObserver) (protocol.Response, error)
}

type ProbeOptions struct {
	WindowID int
	// KittyListenOn identifies the Kitty instance that owns WindowID.
	KittyListenOn string
	// RuntimeSessionID names the session to probe when it has no Kitty
	// window; it takes the place of WindowID.
	RuntimeSessionID string
	Harness          string
	TraceID          string
	RequestID        string
	AMXBin           string
}

type ProbeStage struct {
	Stage     string  `json:"stage"`
	Attempt   int     `json:"attempt,omitempty"`
	Outcome   string  `json:"outcome"`
	Reason    string  `json:"reason,omitempty"`
	LatencyMS float64 `json:"latency_ms,omitempty"`
}

type ProbeReport struct {
	SchemaVersion  int                               `json:"schema_version"`
	GeneratedAt    time.Time                         `json:"generated_at"`
	WindowID       int                               `json:"window_id"`
	KittySocket    string                            `json:"kitty_socket,omitempty"`
	RuntimeSession string                            `json:"runtime_session_id,omitempty"`
	Harness        string                            `json:"harness,omitempty"`
	TraceID        string                            `json:"trace_id"`
	RequestID      string                            `json:"request_id"`
	Success        bool                              `json:"success"`
	Stages         []ProbeStage                      `json:"stages"`
	Delivery       *protocol.DeliveryAcknowledgement `json:"delivery,omitempty"`
	Error          string                            `json:"error,omitempty"`
	Scope          string                            `json:"scope"`
}

// Probe exercises the real bounded transport and daemon routing path using the
// dedicated side-effect-free diagnostic request.
func Probe(client ProbeClient, options ProbeOptions) ProbeReport {
	report := ProbeReport{
		SchemaVersion: ProbeSchemaVersion, GeneratedAt: time.Now().UTC(), WindowID: options.WindowID,
		Harness: strings.TrimSpace(options.Harness), TraceID: strings.TrimSpace(options.TraceID),
		RequestID: strings.TrimSpace(options.RequestID), Stages: make([]ProbeStage, 0), Scope: ProbeScope,
	}
	if known := harness.NormalizeKnown(report.Harness); known != "" {
		report.Harness = known
	}
	if report.TraceID == "" {
		report.TraceID = diagnostics.NewTraceID()
	}
	if report.RequestID == "" {
		report.RequestID = diagnostics.NewTraceID()
	}
	if client == nil {
		report.Error = "probe client is unavailable"
		return report
	}
	report.RuntimeSession = strings.TrimSpace(options.RuntimeSessionID)
	if report.RuntimeSession != "" {
		report.WindowID = 0
	} else if report.WindowID <= 0 {
		report.Error = "a runtime session ID or a positive Kitty window ID is required"
		return report
	} else if !kitty.IsReusableListenAddress(options.KittyListenOn) {
		report.Error = "a reusable Kitty socket is required to probe a Kitty window"
		return report
	} else {
		report.KittySocket = strings.TrimSpace(options.KittyListenOn)
	}
	request := protocol.Request{
		Type: protocol.TypeDiagnosticProbe, WindowID: report.WindowID, KittyListenOn: report.KittySocket, RuntimeSessionID: report.RuntimeSession,
		ProducerHarness: report.Harness, Harness: report.Harness,
		TraceID: report.TraceID, RequestID: report.RequestID,
	}
	response, err := client.SendWithAutoStartObserved(request, options.AMXBin, func(event ipc.DeliveryStageEvent) {
		report.Stages = append(report.Stages, ProbeStage{
			Stage: event.Stage, Attempt: event.Attempt, Outcome: event.Outcome,
			Reason: event.Reason, LatencyMS: event.LatencyMS,
		})
	})
	if err != nil {
		report.Error = err.Error()
		report.Stages = append(report.Stages, ProbeStage{Stage: "delivery", Outcome: "error", Reason: err.Error()})
		return report
	}
	report.Delivery = response.Delivery
	outcome, reason := "success", "routing_discovered"
	if !response.OK {
		outcome, reason = "rejected", response.Error
	}
	if response.Delivery != nil {
		if response.Delivery.Status != "" {
			outcome = response.Delivery.Status
		}
		if response.Delivery.Reason != "" {
			reason = response.Delivery.Reason
		}
	}
	report.Stages = append(report.Stages, ProbeStage{Stage: "daemon_acknowledgement", Outcome: outcome, Reason: reason})
	report.Success = response.OK && response.Delivery != nil && response.Delivery.Status == protocol.DeliveryAcknowledgementAccepted
	if !report.Success {
		report.Error = response.Error
		if report.Error == "" {
			report.Error = fmt.Sprintf("probe acknowledgement was %s", outcome)
		}
	}
	return report
}
