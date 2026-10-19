package tmux

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// SearchContext is read-only copy-mode state. Query is data, never a command.
type SearchContext struct {
	Scope string `json:"scope"`
	Query string `json:"query"`
}

// ReadSearchContext names the same generation-checked pane as the session.
// A format's fixed first line qualifies the rest, which is the literal search
// string (including whitespace and format-looking characters).
func (b *Backend) ReadSearchContext(record runtimesession.Record) (SearchContext, error) {
	if record.Target == nil {
		return SearchContext{}, fmt.Errorf("session has no terminal")
	}
	ref := backend.PaneID(record.Target.Ref)
	if err := paneOnly(ref); err != nil {
		return SearchContext{}, err
	}
	format := "#{pane_id}\t#{pid}\t#{start_time}\t#{alternate_on}\t#{pane_in_mode}\t#{" + varsOption + "}\t#{pane_dead}\n#{pane_search_string}"
	out, err := b.server.run([]string{"display-message", "-p", "-t", ref.ID, format})
	if err != nil {
		return SearchContext{}, err
	}
	head, query, ok := strings.Cut(out, "\n")
	fields := strings.Split(head, "\t")
	if !ok || len(fields) != 7 || fields[0] != ref.ID || b.server.instance(fields[1], fields[2]) != record.Target.Instance || fields[6] != "0" {
		return SearchContext{}, fmt.Errorf("session terminal changed while reading search")
	}
	vars, err := decodeVars(fields[5])
	if err != nil || vars["amx_runtime_session_id"] != string(record.ID) {
		return SearchContext{}, fmt.Errorf("session no longer owns the searched terminal")
	}
	scope := "terminal_history"
	if fields[3] == "1" {
		scope = "visible_screen"
	}
	if fields[4] != "1" {
		query = ""
	} else {
		query = strings.TrimSuffix(query, "\n")
	}
	return SearchContext{Scope: scope, Query: query}, nil
}
