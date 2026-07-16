package harness

import "fmt"

const copilotForkUnavailableReason = "Copilot does not support native forks; portable fork not yet available"

var allNativeInvocationCapabilities = InvocationCapabilities{
	Fresh:  OperationCapability{Supported: true},
	Resume: OperationCapability{Supported: true},
	Fork:   OperationCapability{Supported: true},
}

var copilotInvocationCapabilities = InvocationCapabilities{
	Fresh:  OperationCapability{Supported: true},
	Resume: OperationCapability{Supported: true},
	Fork:   OperationCapability{Reason: copilotForkUnavailableReason},
}

// BuildInvocation delegates CLI-specific argv ordering to the named harness.
// Lookup retains the package's existing fallback-to-Claude behavior for an
// unknown harness name.
func BuildInvocation(name string, kind InvocationKind, input InvocationInput) (Invocation, error) {
	return Lookup(name).BuildInvocation(kind, input)
}

func newInvocation(h Impl, input InvocationInput, args []string) Invocation {
	return Invocation{
		Harness:       h.Name(),
		Executable:    h.Executable(),
		Args:          append([]string(nil), args...),
		Environment:   cloneEnvironment(input.Environment),
		CWD:           input.CWD,
		InitialPrompt: input.InitialPrompt,
		SessionID:     input.SessionID,
		LaunchID:      input.NativeLogging.LaunchID,
		NativeLogging: input.nativeLogging,
	}
}

func cloneEnvironment(environment map[string]string) map[string]string {
	if len(environment) == 0 {
		return map[string]string{}
	}
	clone := make(map[string]string, len(environment))
	for key, value := range environment {
		clone[key] = value
	}
	return clone
}

func requireSessionID(h Impl, kind InvocationKind, input InvocationInput) error {
	if input.SessionID == "" {
		return fmt.Errorf("%s %s requires a session ID", h.Name(), kind)
	}
	return nil
}

func unsupportedInvocation(h Impl, kind InvocationKind) error {
	capability := h.InvocationCapabilities().For(kind)
	return &UnsupportedInvocationError{
		Harness: h.Name(),
		Kind:    kind,
		Reason:  capability.Reason,
	}
}

func withPrompt(args []string, prompt string) []string {
	if prompt == "" {
		return args
	}
	return append(args, prompt)
}
