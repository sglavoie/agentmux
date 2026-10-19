package tmux

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func TestReadSearchContextPreservesQueryAndChecksOwnership(t *testing.T) {
	f := newFixture(t, Options{})
	l := f.launch(t, "search-context", []string{"/bin/sh", "-c", "printf 'ready\\n'; exec sleep 120"}, nil)
	id := runtimesession.ID("rs-search-test")
	if err := f.backend.SetMetadata(l.Pane, map[string]string{"amx_runtime_session_id": string(id)}); err != nil {
		t.Fatal(err)
	}
	record := runtimesession.Record{ID: id, Target: &runtimesession.Target{Instance: l.Instance, Ref: l.Pane.ID}}
	query := `literal #{pane_id} $(touch nope); "quote" 🦊`
	f.tmux(t, "copy-mode", "-t", l.Pane.ID)
	f.tmux(t, "send-keys", "-t", l.Pane.ID, "-X", "search-backward", "--", query)
	got, err := f.backend.ReadSearchContext(record)
	if err != nil || got.Scope != "terminal_history" || got.Query != query {
		t.Fatalf("context=%+v err=%v", got, err)
	}
	f.tmux(t, "send-keys", "-t", l.Pane.ID, "-X", "cancel")
	got, err = f.backend.ReadSearchContext(record)
	if err != nil || got.Query != "" {
		t.Fatalf("closed search leaked old query: %+v %v", got, err)
	}
	wrong := record
	wrongTarget := *record.Target
	wrongTarget.Instance.Generation = "another"
	wrong.Target = &wrongTarget
	if _, err := f.backend.ReadSearchContext(wrong); err == nil {
		t.Fatal("reused server accepted")
	}
	if err := f.backend.SetMetadata(l.Pane, map[string]string{"amx_runtime_session_id": "another"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.backend.ReadSearchContext(record); err == nil {
		t.Fatal("replaced session accepted")
	}
}
