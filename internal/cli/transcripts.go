package cli

import (
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TranscriptReaders is the harness-keyed reader set a command uses to discover
// and read source transcripts. It is part of the runtime a command assembles,
// like the validated configuration and the Kitty client, and the session,
// worktree, and transcripts command families all need the same set: a family
// that built its own would silently index a different corpus than the one
// `amx transcripts rebuild` maintains.
func TranscriptReaders() map[string]transcript.Reader {
	return map[string]transcript.Reader{
		harness.Claude:  transcript.NewClaudeReader(),
		harness.Copilot: transcript.NewCopilotReader(),
		harness.Codex:   transcript.NewCodexReader(),
	}
}
