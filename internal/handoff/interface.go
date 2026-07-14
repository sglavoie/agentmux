// Package handoff builds deterministic, bounded Markdown context packs and
// persists private draft/final artifacts as standalone files.
package handoff

import (
	"context"
	"errors"
	"time"

	"github.com/sglavoie/agentmux/internal/transcript"
)

const (
	DefaultMaxBytes       = 64 * 1024
	DefaultRecentMessages = 12
	DefaultGitTimeout     = 2 * time.Second
	MinimumMaxBytes       = 512
)

var ErrBudgetTooSmall = errors.New("handoff: byte budget is too small for required context")

// Source is the complete point-in-time input to a handoff pack. Transcript
// messages and Git state rooted at CWD are the source authorities.
type Source struct {
	ProjectLabel   string
	Harness        string
	SessionID      string
	CapturedAt     time.Time
	CWD            string
	TranscriptPath string
	Messages       []transcript.Message
}

// Options controls bounded content selection. Zero values use package defaults.
type Options struct {
	MaxBytes       int
	RecentMessages int
	GitTimeout     time.Duration
}

func DefaultOptions() Options {
	return Options{
		MaxBytes:       DefaultMaxBytes,
		RecentMessages: DefaultRecentMessages,
		GitTimeout:     DefaultGitTimeout,
	}
}

// GitState is a best-effort repository snapshot. Available is false for both
// non-Git directories and failed probes; Kind and Detail document which case
// occurred so generation never fails merely because Git is unavailable.
type GitState struct {
	Available bool
	Kind      string
	Detail    string
	Branch    string
	Head      string
	Status    string
	DiffStat  string
}

// Omission records content excluded from the pack and why.
type Omission struct {
	Kind   string
	Detail string
}

// Pack is the immutable result of Build. Content includes the same omissions
// and source paths exposed structurally here.
type Pack struct {
	Content     []byte
	ContentHash string
	Git         GitState
	SourcePaths []string
	Omissions   []Omission
}

// GitInspector exists so tests and callers that already captured repository
// state can make Build independent of subprocess timing.
type GitInspector interface {
	Inspect(context.Context, string, time.Duration) GitState
}

// Builder creates packs without writing files or launching a harness.
type Builder struct {
	git GitInspector
}

func NewBuilder() *Builder { return &Builder{git: commandGitInspector{}} }

func NewBuilderWithGit(inspector GitInspector) *Builder {
	if inspector == nil {
		inspector = commandGitInspector{}
	}
	return &Builder{git: inspector}
}
