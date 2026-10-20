import Foundation
import os

/// What the amx lookup asks of the app, and tells it.
@MainActor
protocol AMXLookupDelegate: AnyObject {
    /// The amx in use now, which a probe's answer is matched against.
    var command: AMXCommand? { get }
    /// Whether what that amx offers is known already: an unanswered probe
    /// is asked again only while it is not.
    var knowsCapabilities: Bool { get }
    /// The environment overrides Settings names, applied over the login
    /// shell's environment.
    var environmentOverrides: [String: String] { get }
    /// A resolve finished: the amx it found, or why there is none.
    func lookup(_ lookup: AMXLookup, found: AMXLookup.Found)
    /// The amx found has been asked what it offers: `capabilities` is nil
    /// when it did not answer (`capabilityProblem` says so once the retries
    /// are spent).
    func lookup(_ lookup: AMXLookup, capabilities: AMXCapabilities?)
    /// `amx backend status`, for Settings and the launch destination.
    func lookup(_ lookup: AMXLookup, backend: BackendSetup)
    /// The amx found is being probed; the app may now compare it with the
    /// daemon's build.
    func lookupStartedProbing(_ lookup: AMXLookup)
}

/// Finds the amx the app runs: reads the login shell's environment once,
/// asks the daemon which build it runs, and resolves amx against both, off
/// the main thread. It owns which resolve is the current one, the bounded
/// retries of a resolve that found nothing or a probe amx did not answer,
/// and the debounce of a resolve Settings asks for. Publishing what it finds
/// is the app's (`AppDelegate+AMX.swift`).
@MainActor
final class AMXLookup {
    nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    struct Found {
        let resolution: Result<AMXBinaryResolver.Resolution, AMXBinaryResolver.NotFound>
        let command: AMXCommand?
        let version: AMXVersion?
        let report: AMXBuildReport?
        let shell: LaunchEnvironment
        let build: DaemonBuild?
    }

    /// How long a resolve that found no amx, or a probe amx did not answer,
    /// waits before it is tried again: an amx installed, or a machine less
    /// busy, a moment later is picked up without a relaunch. Bounded, so an
    /// amx that is not coming is not looked for all day.
    nonisolated static let retryDelays: [TimeInterval] = [2, 5, 15, 30, 60]

    weak var delegate: AMXLookupDelegate?

    /// Which resolve is the current one: what an older, slower one finds is
    /// dropped.
    private(set) var generation = 0
    private(set) var running = false
    /// The daemon build the current amx was chosen against; nil when no
    /// daemon answered then.
    private(set) var resolvedDaemonBuild: DaemonBuild?
    /// The login shell's environment, read once at launch; overrides are
    /// applied on top of it each time amx is resolved again.
    private(set) var detectedEnvironment: LaunchEnvironment?
    /// Why what amx offers is not known, once every probe went unanswered.
    var capabilityProblem: String?

    /// The resolve or probe retry waiting its turn.
    private var pendingRetry: DispatchWorkItem?
    private var pendingResolve: DispatchWorkItem?
    private var resolveRetries = 0
    private var probeRetries = 0

    /// Resolving reads the login shell and probes the binary, which can take
    /// seconds, so it runs here, one at a time.
    private let work: QueueWorkRunner
    private let resources: URL?

    init(work: QueueWorkRunner = QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.work", qos: .utility)),
         resources: URL? = Bundle.main.resourceURL) {
        self.work = work
        self.resources = resources
    }

    /// Resolves amx. amx is usable as soon as it is found: what it offers
    /// (`--help`) and the backend report are asked for afterwards, so a slow
    /// answer there holds up only the items that depend on it. `--version`
    /// is read once, by the resolver when it compares copies, and reused.
    ///
    /// `daemonBuild` is the build a live feed reports, which saves asking;
    /// `retry` keeps the count of earlier attempts.
    func resolve(daemonBuild known: DaemonBuild? = nil, retry: Bool = false) {
        pendingRetry?.cancel()
        pendingRetry = nil
        if !retry { resolveRetries = 0 }
        generation += 1
        let generation = self.generation
        running = true
        let detected = detectedEnvironment
        let overrides = delegate?.environmentOverrides ?? [:]
        let resources = self.resources
        work.run({ () -> Found in
            let shell = detected ?? LaunchEnvironment.current()
            let environment = shell.applying(overrides).namingBundledRuntime(resources: resources)
            let build = known ?? DaemonClient().health().build
            let versions = VersionCache()
            var resolver = AMXBinaryResolver.live(searchPath: environment.path, daemonBuild: build)
            resolver.identity = { versions.read($0) }
            let resolution = resolver.resolve()
            var command: AMXCommand?
            var binaryVersion: AMXVersion?
            var report: AMXBuildReport?
            switch resolution {
            case let .success(binary):
                command = AMXCommand(binary: binary.url, launchEnvironment: environment)
                binaryVersion = versions.read(binary.url.path)
                report = AMXBuildReport.of(binary: binary.url, version: binaryVersion)
                Self.logger.notice("running \(binary.url.path, privacy: .public) (\(String(describing: binary.source), privacy: .public))")
            case let .failure(notFound):
                Self.logger.error("\(notFound.message, privacy: .public)")
            }
            return Found(resolution: resolution, command: command, version: binaryVersion, report: report, shell: shell, build: build)
        }, onMain: { [weak self] found in
            guard let self, generation == self.generation else { return }
            self.finish(found, generation: generation)
        })
    }

    /// Hands the app what a resolve found, then asks the amx it found what
    /// it offers, or schedules another look when it found none.
    private func finish(_ found: Found, generation: Int) {
        running = false
        resolvedDaemonBuild = found.build
        detectedEnvironment = found.shell
        delegate?.lookup(self, found: found)
        guard generation == self.generation else { return }
        guard let command = found.command else {
            scheduleRetry(attempt: &resolveRetries, what: "resolving amx again") { $0.resolve(retry: true) }
            return
        }
        resolveRetries = 0
        probeRetries = 0
        probe(command, generation: generation, backend: true)
        delegate?.lookupStartedProbing(self)
    }

    /// Asks `amx` what it offers, then (with `backend`) for its backend
    /// report, handing each over as it comes. An unanswered probe is asked
    /// again, a few times, rather than read as an amx that lacks everything.
    private func probe(_ amx: AMXCommand, generation: Int, backend: Bool) {
        work.run({ AMXCapabilities.probe(amx) }, onMain: { [weak self] capabilities in
            self?.finishProbe(capabilities, generation: generation)
        })
        guard backend else { return }
        work.run({ () -> BackendSetup in
            let setup = BackendSetup(BackendStatus.fetch(amx))
            AMXLookup.log(setup)
            return setup
        }, onMain: { [weak self] setup in
            guard let self, generation == self.generation else { return }
            self.delegate?.lookup(self, backend: setup)
        })
    }

    private func finishProbe(_ capabilities: AMXCapabilities?, generation: Int) {
        guard generation == self.generation, let delegate, let command = delegate.command else { return }
        if capabilities != nil {
            probeRetries = 0
            capabilityProblem = nil
        } else if !delegate.knowsCapabilities {
            Self.logger.notice("\(command.binary.path, privacy: .public) did not answer --help")
            let scheduled = scheduleRetry(attempt: &probeRetries, what: "asking amx what it offers again") {
                guard generation == $0.generation, let amx = $0.delegate?.command else { return }
                $0.probe(amx, generation: generation, backend: false)
            }
            if !scheduled {
                capabilityProblem = "\(command.binary.path) did not say which commands it has; see the log"
            }
        }
        delegate.lookup(self, capabilities: capabilities)
    }

    /// Looks for amx again when none was found and the bounded retries are
    /// spent: an amx installed since is picked up the next time the operator
    /// opens the menu or asks for the daemon, without a relaunch.
    func lookAgain() {
        guard delegate?.command == nil, !running, pendingRetry == nil else { return }
        Self.logger.notice("no amx yet; looking again")
        resolve()
    }

    /// Resolves again once typing in Preferences pauses, so an environment
    /// value typed one key at a time does not run amx per key.
    func scheduleResolve(after delay: TimeInterval = 0.6) {
        pendingResolve?.cancel()
        let item = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.resolve() }
        }
        pendingResolve = item
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: item)
    }

    /// Reads `amx backend status` again, behind any resolve already queued;
    /// dropped if amx changed meanwhile.
    func refreshBackend(_ amx: AMXCommand) {
        work.run({ BackendSetup(BackendStatus.fetch(amx)) }, onMain: { [weak self] backend in
            guard let self, let current = self.delegate?.command, current.binary == amx.binary,
                  current.launchEnvironment == amx.launchEnvironment else { return }
            self.delegate?.lookup(self, backend: backend)
        })
    }

    /// Runs `action` after the next of `retryDelays`; false, scheduling
    /// nothing, once they are spent.
    @discardableResult
    private func scheduleRetry(attempt: inout Int, what: String, _ action: @escaping @MainActor (AMXLookup) -> Void) -> Bool {
        guard let delay = Self.retryDelay(attempt: attempt) else { return false }
        attempt += 1
        Self.logger.notice("\(what, privacy: .public) in \(Int(delay))s")
        pendingRetry?.cancel()
        let item = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated {
                guard let self else { return }
                action(self)
            }
        }
        pendingRetry = item
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: item)
        return true
    }

    /// How long the retry after `attempt` earlier ones waits; nil once they
    /// are spent.
    nonisolated static func retryDelay(attempt: Int) -> TimeInterval? {
        retryDelays.indices.contains(attempt) ? retryDelays[attempt] : nil
    }

    /// Records the backend setup amx reported, so a missing or refused tmux is
    /// in the log with its fix, not only in Preferences.
    nonisolated static func log(_ setup: BackendSetup) {
        switch setup {
        case .unknown:
            break
        case let .unavailable(reason):
            logger.error("backend: amx could not report the setup: \(reason, privacy: .public)")
        case let .status(status):
            logger.notice("backend: \(status.tmuxLine, privacy: .public) at \(status.tmux.path, privacy: .public); session server: \(status.serverLine, privacy: .public)")
            for problem in status.problems {
                let line = "backend: \(problem.severity) \(problem.code): \(problem.text)"
                if problem.needsAttention {
                    logger.error("\(line, privacy: .public)")
                } else {
                    logger.notice("\(line, privacy: .public)")
                }
            }
        }
    }
}
