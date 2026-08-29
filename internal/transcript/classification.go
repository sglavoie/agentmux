package transcript

import (
	"encoding/json"
	"strings"
)

// sessionClassEvidence accumulates markers across a transcript. Automated
// evidence always wins, followed by positive interactive evidence; a file
// with neither remains unknown.
type sessionClassEvidence struct {
	automated bool
	main      bool
}

func (e sessionClassEvidence) class() SessionClass {
	if e.automated {
		return SessionClassAutomated
	}
	if e.main {
		return SessionClassMain
	}
	return SessionClassUnknown
}

func normalizedSessionClass(class SessionClass) SessionClass {
	switch class {
	case SessionClassMain, SessionClassAutomated, SessionClassUnknown:
		return class
	default:
		return SessionClassUnknown
	}
}

func normalizedMarker(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func codexSourceMarker(raw json.RawMessage) (name string, subagent bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		name = normalizedMarker(text)
		return name, name == "subagent"
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", false
	}
	_, subagent = object["subagent"]
	return "", subagent
}

func observeCodexSessionMeta(e *sessionClassEvidence, originator string, source json.RawMessage) {
	origin := normalizedMarker(originator)
	sourceName, subagent := codexSourceMarker(source)
	if subagent || sourceName == "exec" || origin == "codex_exec" ||
		origin == "agent-session-loop" || origin == "asl" {
		e.automated = true
	}
	if sourceName == "cli" || sourceName == "vscode" ||
		origin == "codex-tui" || origin == "codex_cli_rs" || origin == "codex desktop" {
		e.main = true
	}
}

func observeClaudeRecord(e *sessionClassEvidence, rec claudeRecord) {
	entrypoint := normalizedMarker(rec.Entrypoint)
	promptSource := normalizedMarker(rec.PromptSource)
	if rec.IsSidechain || rec.AgentID != "" || entrypoint == "sdk-cli" || promptSource == "sdk" {
		e.automated = true
	}
	if entrypoint == "cli" || promptSource == "typed" {
		e.main = true
	}
}
