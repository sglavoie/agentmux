package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func sharedProjectAliasMeta() transcript.SessionMeta {
	return transcript.SessionMeta{
		ID:           "shared-session",
		Harness:      harness.Claude,
		Profile:      "work",
		Profiles:     []string{"work", "personal"},
		Project:      "SS",
		Projects:     []string{"SS", "streamutil"},
		CWD:          "/tmp/shared-project",
		StartedAt:    time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC),
		LastAt:       time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC),
		Path:         "/tmp/shared-project/session.jsonl",
		MessageCount: 2,
		Snippet:      "shared directory transcript",
	}
}

func sharedProjectAliasModel() Model {
	meta := sharedProjectAliasMeta()
	return Model{
		mode:          ModeSessionSearch,
		width:         120,
		height:        30,
		filterProject: []string{"streamutil"},
		cfg: &config.Config{Accounts: []config.Account{
			{Name: "work", Harness: harness.Claude},
			{Name: "personal", Harness: harness.Claude},
		}},
		transcriptReaders: map[string]transcript.Reader{
			harness.Claude: sessionSearchTestReader{harness: harness.Claude},
		},
		sessionResults: []transcript.Result{{Meta: meta, Preview: meta.Snippet}},
	}
}

func TestContextualSessionMetaCopiesOnlyMatchingSingleProjectAlias(t *testing.T) {
	meta := sharedProjectAliasMeta()
	original := meta
	want := meta
	want.Project = "streamutil"

	got := (Model{filterProject: []string{"streamutil"}}).contextualSessionMeta(meta)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contextual metadata = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(meta, original) {
		t.Fatalf("contextualization mutated source metadata: before %#v after %#v", original, meta)
	}

	for _, tc := range []struct {
		name   string
		filter []string
	}{
		{name: "unfiltered"},
		{name: "multiple", filter: []string{"SS", "streamutil"}},
		{name: "non-member", filter: []string{"other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Model{filterProject: tc.filter}).contextualSessionMeta(meta); !reflect.DeepEqual(got, meta) {
				t.Fatalf("contextual metadata = %#v, want unchanged %#v", got, meta)
			}
		})
	}
}

func TestSharedProjectAliasRenderingAndViewerDoNotMutateStoredResult(t *testing.T) {
	m := sharedProjectAliasModel()
	stored := m.sessionResults[0]

	unfiltered := m
	unfiltered.filterProject = nil
	unfilteredRow := stripAnsi(renderSessionSearchList(unfiltered, 10))
	if !strings.Contains(unfilteredRow, "SS") || strings.Contains(unfilteredRow, "streamutil") {
		t.Fatalf("unfiltered row did not use stable primary project:\n%s", unfilteredRow)
	}

	filteredRow := stripAnsi(renderSessionSearchList(m, 10))
	if strings.Contains(filteredRow, "streamutil") || strings.Contains(filteredRow, "SS") {
		t.Fatalf("filtered row repeated the selected project or its primary alias:\n%s", filteredRow)
	}
	if view := stripAnsi(renderSessionSearch(m)); !strings.Contains(view, "[r] project: streamutil") {
		t.Fatalf("filtered view did not retain selected project chip:\n%s", view)
	}
	if !reflect.DeepEqual(m.sessionResults[0], stored) {
		t.Fatalf("rendering mutated stored result: before %#v after %#v", stored, m.sessionResults[0])
	}

	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if cmd == nil || got.mode != ModeSessionView || got.viewerSession == nil {
		t.Fatalf("enter = mode %v viewer %+v cmd %T, want loading transcript viewer", got.mode, got.viewerSession, cmd)
	}
	if got.viewerSession.Project != "streamutil" || got.viewerSession.ID != stored.Meta.ID || got.viewerSession.CWD != stored.Meta.CWD || got.viewerSession.Path != stored.Meta.Path {
		t.Fatalf("viewer metadata = %+v, want contextual alias with stable identity and paths", got.viewerSession)
	}
	got.viewerLoading = false
	got.viewerMessages = []transcript.Message{{Role: "user", Text: "hello"}}
	view := stripAnsi(renderSessionView(got))
	if !strings.Contains(view, "Transcript: streamutil") {
		t.Fatalf("viewer header did not retain selected alias:\n%s", view)
	}
	if status := sessionViewStatusText(got); !strings.Contains(status, "Transcript: streamutil") {
		t.Fatalf("viewer status = %q, want selected alias", status)
	}
	if !reflect.DeepEqual(m.sessionResults[0], stored) {
		t.Fatalf("viewer navigation mutated stored result: before %#v after %#v", stored, m.sessionResults[0])
	}
}

func TestSharedProjectAliasSelectionTargetsEveryDeferredAction(t *testing.T) {
	base := sharedProjectAliasModel()
	stored := base.sessionResults[0]

	updated, cmd := base.Update(keyPress("w"))
	worktree := updated.(Model)
	if cmd != nil || worktree.mode != ModeConfirm || worktree.isolatedForkConversation == nil {
		t.Fatalf("worktree fork = mode %v conversation %+v cmd %T", worktree.mode, worktree.isolatedForkConversation, cmd)
	}
	if worktree.isolatedForkConversation.Project != "streamutil" || worktree.isolatedForkConversation.ID != stored.Meta.ID || worktree.isolatedForkConversation.CWD != stored.Meta.CWD {
		t.Fatalf("worktree conversation = %+v, want alias with stable identity and CWD", worktree.isolatedForkConversation)
	}
	if worktree.targetSession.Project != "streamutil" || worktree.targetSession.Directory != stored.Meta.CWD {
		t.Fatalf("worktree target = %+v, want contextual alias and source directory", worktree.targetSession)
	}

	for _, tc := range []struct {
		key      string
		wantEdit bool
	}{
		{key: "H"},
		{key: "E", wantEdit: true},
	} {
		t.Run("handoff_"+tc.key, func(t *testing.T) {
			updated, cmd := base.Update(keyPress(tc.key))
			got := updated.(Model)
			if cmd != nil || got.mode != ModeConfigPicker || got.handoffConversation == nil {
				t.Fatalf("%s = mode %v conversation %+v cmd %T", tc.key, got.mode, got.handoffConversation, cmd)
			}
			if got.handoffConversation.Project != "streamutil" || got.handoffConversation.ID != stored.Meta.ID || got.handoffConversation.CWD != stored.Meta.CWD || got.handoffEdit != tc.wantEdit {
				t.Fatalf("%s handoff target = %+v edit=%v, want contextual alias with stable source", tc.key, got.handoffConversation, got.handoffEdit)
			}
			if got.targetSession.Project != "streamutil" || got.targetSession.Directory != stored.Meta.CWD {
				t.Fatalf("%s picker target = %+v, want contextual alias and source directory", tc.key, got.targetSession)
			}
		})
	}

	updated, cmd = base.Update(keyPress("d"))
	deleted := updated.(Model)
	if cmd != nil || deleted.mode != ModeDeleteChoice || deleted.deleteTarget == nil {
		t.Fatalf("delete = mode %v target %+v cmd %T", deleted.mode, deleted.deleteTarget, cmd)
	}
	if deleted.deleteTarget.Project != "streamutil" || deleted.deleteTarget.ID != stored.Meta.ID || deleted.deleteTarget.Path != stored.Meta.Path {
		t.Fatalf("delete target = %+v, want contextual alias with stable identity and source", deleted.deleteTarget)
	}
	if prompt := stripAnsi(renderDeleteChoice(deleted)); !strings.Contains(prompt, "streamutil") {
		t.Fatalf("delete prompt did not use contextual alias:\n%s", prompt)
	}

	if !reflect.DeepEqual(base.sessionResults[0], stored) {
		t.Fatalf("selection actions mutated stored result: before %#v after %#v", stored, base.sessionResults[0])
	}
}
