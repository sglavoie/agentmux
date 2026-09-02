package events

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// BuildHookStateUpdateRequest turns a decoded hook payload into the state
// update request the reducer path consumes. Every field the reducer reads is
// derived here and nowhere else, which is why the parameter list is long: the
// alternative is a partially populated request that some later caller has to
// remember to finish.
func BuildHookStateUpdateRequest(
	windowID int,
	action string,
	eventOrAction string,
	payload hookevent.HookPayload,
	sessionTitle string,
	profile string,
	producerHarness string,
	targetHarness string,
	currentIconState string,
	pipeMode bool,
	preservesIcon bool,
	contextLimitDetected bool,
	resetFlowDetected bool,
	kittyListenOn string,
) protocol.Request {
	return protocol.Request{
		Type:             protocol.TypeStateUpdate,
		WindowID:         windowID,
		Action:           action,
		Event:            eventOrAction,
		ToolName:         payload.EffectiveToolName(),
		Source:           payload.Source,
		IsPrompt:         hookevent.IsPromptEvent(eventOrAction),
		PreservesIcon:    preservesIcon,
		PipeMode:         pipeMode,
		KittyListenOn:    strings.TrimSpace(kittyListenOn),
		SessionTitle:     sessionTitle,
		Account:          profile,
		Harness:          harness.NormalizeKnown(producerHarness),
		TargetHarness:    harness.NormalizeKnown(targetHarness),
		TargetHarnessSet: true,
		CurrentIconState: currentIconState,
		NotificationType: payload.EffectiveNotificationType(),
		StopReason:       payload.EffectiveStopReason(),
		Reason:           payload.Reason,
		ContextLimit:     contextLimitDetected,
		ResetFlow:        resetFlowDetected,
		QuestionPreview:  payload.QuestionPreview,
	}
}
