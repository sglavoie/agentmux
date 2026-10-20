import AppKit

// Publishing what `AMXLookup` finds: the amx commands run through, what it
// offers, and its backend report, into Preferences, the menu, and the
// workspace.
extension AppDelegate: AMXLookupDelegate {
    var knowsCapabilities: Bool { preferences.capabilities != nil }

    var environmentOverrides: [String: String] { preferencesModel.preferences.environment }

    func resolveBinary(daemonBuild: DaemonBuild? = nil) {
        lookup.resolve(daemonBuild: daemonBuild)
    }

    /// Publishes what a resolve found.
    func lookup(_ lookup: AMXLookup, found: AMXLookup.Found) {
        let command = found.command
        let same = self.command?.binary == command?.binary && self.command?.launchEnvironment == command?.launchEnvironment
        if !same {
            projects.state.reset()
            projects.missingDirectories = []
            recent.state.reset()
        }
        var snapshot = PreferencesSnapshot()
        snapshot.detectedEnvironment = found.shell
        if case let .failure(notFound) = found.resolution {
            snapshot.binaryProblem = notFound.message
            snapshot.binaryMenuProblem = notFound.menuMessage
        } else {
            snapshot.binary = try? found.resolution.get()
            snapshot.binaryVersion = found.version
            // The same amx keeps what it was found to offer until it is
            // asked again.
            if same, command != nil {
                snapshot.capabilities = preferences.capabilities
                snapshot.backend = preferences.backend
            } else {
                lookup.capabilityProblem = nil
            }
        }
        self.command = command
        workspace?.retryTerminal()
        let waiting = statusQuestions
        statusQuestions = []
        for (id, completion) in waiting {
            if command == nil {
                completion(.unknown(snapshot.binaryMenuProblem ?? AMXWording.notFound))
            } else {
                sessionStatus(runtimeSessionID: id, completion: completion)
            }
        }
        // Doctor reads the build an app reports only to compare it with the
        // daemon's; the daemon decides nothing by it. A live stream is kept,
        // and the next subscribe carries it.
        if subscribeRequest.report(build: found.report), store.feed == .live {
            Self.logger.notice("amx build changed; reported at the next subscribe")
        }
        snapshot.daemonStatus = store.daemonStatus
        preferences = snapshot
        publishPreferences()
        statusItem?.refresh()
        watchConfig()
        // The first subscribe waited for this, so it reports the build.
        startSubscription()
    }

    func lookupStartedProbing(_ lookup: AMXLookup) {
        followDaemonBuild()
    }

    func lookup(_ lookup: AMXLookup, capabilities: AMXCapabilities?) {
        if let capabilities { preferences.capabilities = capabilities }
        publishPreferences()
        statusItem?.refresh()
        statusItem?.refreshSubmenus()
        // Listed and surveyed once the gate knows what this amx can do, so
        // the first menu opening already knows which projects can be
        // launched.
        refreshProjects()
    }

    func lookup(_ lookup: AMXLookup, backend: BackendSetup) {
        guard preferences.backend != backend else { return }
        preferences.backend = backend
        publishPreferences()
    }

    /// Looks for amx again when none was found and the bounded retries are
    /// spent.
    func lookAgainForAmx() {
        lookup.lookAgain()
    }

    /// Resolves again when the live daemon runs another build than the one
    /// amx was chosen against: one started (or replaced) since may be the
    /// artifact `~/.local/bin/amx` is, which then runs instead of the
    /// bundled copy.
    func followDaemonBuild() {
        guard store.feed == .live, !lookup.running, let build = store.snapshot?.daemon.build,
              build != lookup.resolvedDaemonBuild else { return }
        Self.logger.notice("daemon runs build \(build.version, privacy: .public) (\(build.revision ?? "unknown", privacy: .public)); resolving amx again")
        resolveBinary(daemonBuild: build)
    }

    /// Reads `amx backend status` again for Settings, which otherwise shows
    /// what the last resolve saw.
    func refreshBackendStatus() {
        guard let amx = command else { return }
        lookup.refreshBackend(amx)
    }
}
