package debug

import (
	"fmt"
	"io"
	"strings"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// checkCompanionApp is the stable identifier for a connected companion app. It
// is informational: an app being connected is a fact worth seeing beside the
// daemon, never a problem.
const checkCompanionApp = "companion_app"

// checkCompanionBuildMismatch is the stable identifier for a companion app
// whose bundled amx is not the artifact the daemon runs. Its severity is
// warning rather than failure, unlike the CLI's own build_mismatch: the app
// talks to the daemon over a versioned protocol and keeps working, but the amx
// it shells out to for actions may disagree with the daemon about the rest.
const checkCompanionBuildMismatch = "companion_build_mismatch"

// writeDoctorCompanion prints the companion line and raises one companion_app
// finding per connected app, plus companion_build_mismatch for each app whose
// reported amx build is not the daemon's artifact. `amx debug subscribe` is
// not an app — it is this CLI looking at the stream — so it is neither
// reported nor compared; the CLI's own identity is already build_mismatch's
// subject. An app that reports no amx build, or a daemon that reports none,
// is not compared either: there is nothing to say it differs.
func writeDoctorCompanion(out io.Writer, companion *protocol.CompanionHealth, daemonBuild *buildinfo.Info, addFinding func(string, string, string, ...any)) error {
	if companion == nil {
		_, err := fmt.Fprintln(out, "companion: unknown (daemon predates the companion protocol)")
		return err
	}
	var apps []protocol.CompanionClient
	for _, client := range companion.Clients {
		if client.Client != subscribeClientName {
			apps = append(apps, client)
		}
	}
	if len(apps) == 0 {
		_, err := fmt.Fprintln(out, "companion: no app connected")
		return err
	}
	labels := make([]string, 0, len(apps))
	for _, app := range apps {
		label := companionAppLabel(app)
		labels = append(labels, label)
		addFinding(severityInfo, checkCompanionApp, "companion app connected: %s", label)
		if app.AMXBuild != nil && daemonBuild != nil && !app.AMXBuild.SameArtifact(*daemonBuild) {
			addFinding(severityWarning, checkCompanionBuildMismatch,
				"companion app %s bundles amx %s but the daemon runs %s; rebuild or reinstall the app from the same checkout",
				label, app.AMXBuild.String(), daemonBuild.String())
		}
	}
	_, err := fmt.Fprintf(out, "companion: %s\n", strings.Join(labels, ", "))
	return err
}

// companionAppLabel names an app as it identified itself, falling back to its
// subscriber ID when it gave no name.
func companionAppLabel(app protocol.CompanionClient) string {
	name := app.Client
	if name == "" {
		name = app.SubscriberID
	}
	if app.ClientVersion == "" {
		return name
	}
	return name + " " + app.ClientVersion
}
