package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// A Kitty session says which project and profile it runs in its tab's user
// vars, which every Kitty snapshot carries. A session on another backend has
// no tab: its label is the project its registry record names and the profile
// its pane metadata holds, read once per binding and kept beside the slot
// table. Without it a companion could only list such a session by its
// harness, with nothing to group it by.

// runtimeLabelRetry is how long a failed pane read waits before it is tried
// again. A read fails while the pane's server is briefly unreachable; the
// project from the registry is shown meanwhile.
const runtimeLabelRetry = 30 * time.Second

// RuntimeSessionLabel is what a runtime session's terminal says it runs.
type RuntimeSessionLabel struct {
	Project string
	Profile string
	Harness string
	// Directory is the directory the session was launched in.
	Directory string
}

// RuntimeSessionLabeler reads a live runtime session's label from its backend.
// It runs off stateMu and may start a subprocess.
type RuntimeSessionLabeler func(record runtimesession.Record) (RuntimeSessionLabel, error)

// runtimeLabel is one session's label and whether its backend has answered.
type runtimeLabel struct {
	project string
	profile string
	// harness is the harness the session was launched with. It names the
	// session until its first hook names a conversation.
	harness string
	// directory is the directory the session was launched in, as its pane
	// records it: where the Git probe looks for a session with no window.
	directory string
	// color is the palette color picked for the session, empty for none.
	// Unlike the rest it can change while the binding lives, so every
	// reconciliation pass reads it again (observeRuntimeColors).
	color string
	// name is the name the operator gave the session, empty for none. Like
	// the color it can change while the binding lives (observeRuntimeNames).
	name string
	// title is the title of the session's group, which carries its harness
	// icon. The icon picker rewrites it, so like the color it is read again
	// by every pass (observeRuntimeTitles).
	title string
	// read is true once the backend answered (or there is none to ask), so
	// the label is final for as long as the binding lives.
	read    bool
	triedAt time.Time
}

// SetRuntimeSessionLabeler installs the backend read behind runtime-session
// labels. It is called once at startup, before any pass runs; without one a
// session is labeled with its registry project alone.
func (h *Handler) SetRuntimeSessionLabeler(labeler RuntimeSessionLabeler) {
	h.runtimeLabeler = labeler
}

// SetProfileColors installs the configured profiles' palette colors, by
// profile name. It is called once at startup, before any snapshot is built;
// without it a session is colored only when a color was picked for it.
func (h *Handler) SetProfileColors(colors map[string]string) {
	folded := make(map[string]string, len(colors))
	for profile, color := range colors {
		folded[strings.ToLower(profile)] = color
	}
	h.profileColors = folded
}

// SetHarnessIcons installs the configured harness emojis, longest first
// (config.HarnessIcons). It is called once at startup, before any snapshot is
// built; without it no session is listed with a harness icon.
func (h *Handler) SetHarnessIcons(icons []string) {
	h.harnessIcons = slices.Clone(icons)
}

// SetSessionSort installs the configured sort mode, "alpha" or "account". It
// is called once at startup, before any snapshot is built.
func (h *Handler) SetSessionSort(mode string) {
	h.sessionSort = mode
}

// runtimeColorLocked is the palette color a non-Kitty session is shown in:
// the one picked for it, else its profile's. Callers hold stateMu.
func (h *Handler) runtimeColorLocked(label runtimeLabel) string {
	if label.color != "" {
		return label.color
	}
	return h.profileColors[strings.ToLower(label.profile)]
}

// noteRuntimeProjectLocked records the registry's project for a session that
// has just claimed a non-Kitty slot, so it is grouped correctly from its first
// snapshot; the profile follows on the next labeling pass. Callers hold
// stateMu.
func (h *Handler) noteRuntimeProjectLocked(runtime, project string) {
	if _, ok := h.runtimeLabels[runtime]; ok || project == "" {
		return
	}
	if h.runtimeLabels == nil {
		h.runtimeLabels = make(map[string]runtimeLabel)
	}
	h.runtimeLabels[runtime] = runtimeLabel{project: project}
}

// labelRuntimeSessions reads the label of every non-Kitty binding that has
// none yet, off stateMu, and forgets the labels of sessions no longer bound.
// It runs after the checkpoint restore, before the socket binds, so a
// restarted daemon's first snapshot already groups its sessions as the last
// one did, and then on every maintenance pass.
func (h *Handler) labelRuntimeSessions(now time.Time) {
	if h.runtimeDirectory == nil {
		return
	}
	h.stateMu.Lock()
	bound := make(map[string]struct{}, len(h.runtimeRoutes.bySlot))
	var wanted []string
	for _, b := range h.runtimeRoutes.bySlot {
		if b.target.backend == string(runtimesession.BackendKitty) {
			continue
		}
		bound[b.runtime] = struct{}{}
		label, ok := h.runtimeLabels[b.runtime]
		if !ok || (!label.read && now.Sub(label.triedAt) >= runtimeLabelRetry) {
			wanted = append(wanted, b.runtime)
		}
	}
	for runtime := range h.runtimeLabels {
		if _, ok := bound[runtime]; !ok {
			delete(h.runtimeLabels, runtime)
		}
	}
	h.stateMu.Unlock()
	if len(wanted) == 0 {
		return
	}

	labels := make(map[string]runtimeLabel, len(wanted))
	for _, runtime := range wanted {
		id, err := runtimesession.ParseID(runtime)
		if err != nil {
			continue
		}
		record, found, _ := h.runtimeDirectory.Lookup(id)
		if !found {
			continue
		}
		label := runtimeLabel{project: record.Project, read: h.runtimeLabeler == nil, triedAt: now}
		if h.runtimeLabeler != nil {
			read, err := h.runtimeLabeler(record)
			if err != nil {
				slog.Debug("maintenance: runtime session label unread", "runtime_session", runtime, "err", err)
			} else {
				label.read = true
				label.profile = read.Profile
				label.harness = read.Harness
				label.directory = read.Directory
				if read.Project != "" {
					label.project = read.Project
				}
			}
		}
		labels[runtime] = label
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	changed := false
	for runtime, label := range labels {
		if _, still := h.runtimeRoutes.byRuntime[runtime]; !still {
			continue
		}
		if h.runtimeLabels == nil {
			h.runtimeLabels = make(map[string]runtimeLabel)
		}
		old := h.runtimeLabels[runtime]
		if old.project != label.project || old.profile != label.profile || old.harness != label.harness {
			changed = true
		}
		label.color, label.name, label.title = old.color, old.name, old.title
		h.runtimeLabels[runtime] = label
	}
	if changed {
		h.companionDirty()
	}
}

// errRuntimeLabelUnsupported is returned for a backend whose panes carry no
// metadata this daemon can read.
var errRuntimeLabelUnsupported = errors.New("runtime session label: backend has no readable pane metadata")

// TmuxRuntimeSessionLabel reads a tmux runtime session's label from the pane
// metadata its launch wrote: one listing of the private server the registry
// record names. A server that is gone, restarted (another generation), or no
// longer holding the pane is an error; the caller keeps what it has.
func TmuxRuntimeSessionLabel(record runtimesession.Record) (RuntimeSessionLabel, error) {
	if record.Target == nil || record.Target.Instance.Kind != runtimesession.BackendTmux {
		return RuntimeSessionLabel{}, errRuntimeLabelUnsupported
	}
	instance := record.Target.Instance
	// NewServer prepares its directory; never do that for a server that is
	// not there.
	if _, err := os.Stat(instance.Address); err != nil {
		return RuntimeSessionLabel{}, fmt.Errorf("runtime session label: %w", err)
	}
	server, err := tmux.NewServer(tmux.Options{Dir: filepath.Dir(instance.Address)})
	if err != nil {
		return RuntimeSessionLabel{}, err
	}
	if server.Socket() != instance.Address {
		return RuntimeSessionLabel{}, fmt.Errorf("runtime session label: %s is not a private tmux socket", instance.Address)
	}
	current, err := server.Instance()
	if err != nil {
		return RuntimeSessionLabel{}, err
	}
	if current.Generation != instance.Generation {
		return RuntimeSessionLabel{}, fmt.Errorf("runtime session label: tmux server generation %s, registered %s", current.Generation, instance.Generation)
	}
	panes, err := tmux.NewBackend(server).Discover()
	if err != nil {
		return RuntimeSessionLabel{}, err
	}
	for _, pane := range panes {
		if pane.Ref.ID != record.Target.Ref {
			continue
		}
		if claimed := pane.Vars["amx_runtime_session_id"]; claimed != "" && claimed != string(record.ID) {
			return RuntimeSessionLabel{}, fmt.Errorf("runtime session label: pane %s belongs to %s", pane.Ref.ID, claimed)
		}
		profile := pane.Vars["amx_profile"]
		if profile == "" {
			profile = pane.Vars["cc_account"]
		}
		directory := pane.Vars["amx_directory"]
		if directory == "" {
			directory = pane.Vars["cc_cwd"]
		}
		return RuntimeSessionLabel{Project: pane.Vars["amx_project"], Profile: profile, Harness: pane.Vars["amx_harness"], Directory: directory}, nil
	}
	return RuntimeSessionLabel{}, fmt.Errorf("runtime session label: pane %s is not on the server", record.Target.Ref)
}
