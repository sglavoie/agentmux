package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestDeleteChoiceMutationWaitsForCacheLockOutsideUpdate(t *testing.T) {
	m := sessionSearchFilterModel(t)
	target := m.sessionResults[0].Meta
	m.mode = ModeDeleteChoice
	m.deleteTarget = &target

	lockHeld := make(chan struct{})
	releaseLock := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- cachefile.WithExclusive(m.transcriptIndex.Path(), func() error {
			close(lockHeld)
			<-releaseLock
			return nil
		})
	}()
	select {
	case <-lockHeld:
	case err := <-lockDone:
		t.Fatalf("acquiring cache lock: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out acquiring cache lock")
	}

	type updateResult struct {
		model tea.Model
		cmd   tea.Cmd
	}
	updateDone := make(chan updateResult, 1)
	go func() {
		updated, cmd := m.Update(keyPress("i"))
		updateDone <- updateResult{model: updated, cmd: cmd}
	}()

	var result updateResult
	select {
	case result = <-updateDone:
	case <-time.After(500 * time.Millisecond):
		close(releaseLock)
		<-lockDone
		t.Fatal("delete key blocked Update on the cache advisory lock")
	}
	if got := result.model.(Model); got.mode != ModeSessionSearch {
		close(releaseLock)
		<-lockDone
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if result.cmd == nil {
		close(releaseLock)
		<-lockDone
		t.Fatal("delete key returned nil mutation command")
	}

	mutationDone := make(chan tea.Msg, 1)
	go func() { mutationDone <- result.cmd() }()
	select {
	case msg := <-mutationDone:
		close(releaseLock)
		<-lockDone
		t.Fatalf("mutation completed while cache lock was held: %T", msg)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseLock)
	if err := <-lockDone; err != nil {
		t.Fatalf("releasing cache lock: %v", err)
	}
	select {
	case msg := <-mutationDone:
		if _, ok := msg.(transcriptDeleteDoneMsg); !ok {
			t.Fatalf("mutation command produced %T, want transcriptDeleteDoneMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mutation did not resume after cache lock was released")
	}
}

func TestTranscriptDeletePartialSuccessUsesWarningToast(t *testing.T) {
	m := sessionSearchFilterModel(t)
	target := m.sessionResults[0].Meta
	updated, _ := m.Update(transcriptDeleteDoneMsg{
		target:   target,
		alsoFile: true,
		err: &transcript.SourceDeletedError{
			Path: target.Path,
			Err:  errors.New("disk full"),
		},
	})
	got := updated.(Model)
	if len(got.toasts) != 1 {
		t.Fatalf("toasts = %+v, want one partial-success warning", got.toasts)
	}
	if got.toasts[0].Severity != toastWarning {
		t.Fatalf("toast severity = %v, want warning", got.toasts[0].Severity)
	}
	if text := got.toasts[0].Text; !strings.Contains(text, "Deleted transcript file") || !strings.Contains(text, "index update failed") || strings.Contains(text, "Delete failed") {
		t.Fatalf("partial-success toast = %q", text)
	}
}

func TestSessionsRefreshCmdPrunesOldEntries(t *testing.T) {
	old := transcript.SessionMeta{
		ID:      "old",
		Harness: harness.Claude,
		LastAt:  time.Now().Add(-45 * 24 * time.Hour),
	}
	idx := writeTranscriptIndex(t, map[string]transcript.SessionMeta{})
	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30
	cfg.Profiles = []config.Account{{
		Name:    "work",
		Harness: harness.Claude,
		HomeDir: t.TempDir(),
	}}
	readers := map[string]transcript.Reader{
		harness.Claude: sessionSearchTestReader{
			harness: harness.Claude,
			discover: func(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
				return []transcript.SessionMeta{old}, nil
			},
		},
	}

	msg, ok := drainCmd(t, sessionsRefreshCmd(idx, cfg, readers, 7)).(sessionsRefreshDoneMsg)
	if !ok {
		t.Fatalf("refresh cmd produced %T, want sessionsRefreshDoneMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("refresh/prune returned error: %v", msg.err)
	}
	if msg.seq != 7 {
		t.Fatalf("seq = %d, want 7", msg.seq)
	}
	meta, ok := idx.Get("claude:old")
	if !ok {
		t.Fatal("old entry missing after refresh/prune")
	}
	if !meta.HiddenFromIndex {
		t.Fatal("old entry was not hidden by refresh-time prune")
	}
}

func TestFormatSessionWhenFutureClampsToZero(t *testing.T) {
	future := time.Now().Add(5 * time.Second)
	if got := formatSessionWhen(future); got != " 0m   ago" {
		t.Fatalf("formatSessionWhen(future) = %q, want %q", got, " 0m   ago")
	}
}

func TestSessionProfileLabelCompactsSharedHomeProfiles(t *testing.T) {
	cases := []struct {
		name string
		meta transcript.SessionMeta
		want string
	}{
		{
			name: "legacy profile",
			meta: transcript.SessionMeta{Profile: "work"},
			want: "work",
		},
		{
			name: "shared home",
			meta: transcript.SessionMeta{Profile: "work", Profiles: []string{"work", "personal", "ops"}},
			want: "work+2",
		},
		{
			name: "display profile outside membership",
			meta: transcript.SessionMeta{Profile: "default", Profiles: []string{"work", "personal"}},
			want: "default+2",
		},
		{
			name: "profiles fallback",
			meta: transcript.SessionMeta{Profiles: []string{"work", "personal"}},
			want: "work+1",
		},
		{
			name: "empty",
			meta: transcript.SessionMeta{},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionProfileLabel(tc.meta); got != tc.want {
				t.Fatalf("sessionProfileLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatSessionRowCompactsSharedHomeProfiles(t *testing.T) {
	meta := transcript.SessionMeta{
		ID:       "shared",
		Harness:  harness.Claude,
		Profile:  "work",
		Profiles: []string{"work", "personal", "ops"},
		Project:  "Foo",
		LastAt:   time.Now(),
		Snippet:  "shared transcript",
	}

	out := stripAnsi(formatSessionRow(transcript.Result{Meta: meta, Preview: meta.Snippet}, map[string]string{}, 120))
	if !strings.Contains(out, "[work+2/claude]") {
		t.Fatalf("row did not render compact shared-home profile label:\n%s", out)
	}
	if strings.Contains(out, "[work/claude]") {
		t.Fatalf("row rendered ambiguous primary-only profile label:\n%s", out)
	}
}

func TestRenderSessionViewCompactsSharedHomeProfiles(t *testing.T) {
	meta := transcript.SessionMeta{
		Harness:  harness.Claude,
		Profile:  "work",
		Profiles: []string{"work", "personal", "ops"},
		Project:  "Foo",
		Path:     "/tmp/shared.jsonl",
	}

	out := stripAnsi(renderSessionView(Model{
		width:         100,
		viewerSession: &meta,
	}))
	if !strings.Contains(out, "Transcript: Foo  [work+2/claude]") {
		t.Fatalf("viewer header did not render compact shared-home profile label:\n%s", out)
	}
}
