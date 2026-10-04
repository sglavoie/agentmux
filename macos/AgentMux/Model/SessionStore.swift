import Foundation

/// How the status item's title summarizes the attention counts.
enum StatusTitleStyle: String, CaseIterable, Equatable {
    /// One glyph and count per non-zero state: `⚡2 🛑1 🔑1`.
    case glyphs
    /// The attention total alone: `3`.
    case compact
    /// No text; the icon only.
    case iconOnly
}

/// The status item's title text.
enum StatusTitle {
    /// The states the title counts, in the palette's order.
    static let countedStates = ["bolt", "stop", "key", "error", "question"]

    static func text(
        for attention: CompanionAttention,
        style: StatusTitleStyle = .glyphs,
        showWorking: Bool = true,
        glyphs: Glyphs = .bundled
    ) -> String {
        switch style {
        case .iconOnly:
            return ""
        case .compact:
            return attention.total > 0 ? String(attention.total) : ""
        case .glyphs:
            let counts: [String: Int] = [
                "bolt": showWorking ? attention.working : 0,
                "stop": attention.stop,
                "key": attention.key,
                "error": attention.error,
                "question": attention.question,
            ]
            let order = glyphs.stateOrder.filter(countedStates.contains)
                + countedStates.filter { !glyphs.stateOrder.contains($0) }
            return order.compactMap { state -> String? in
                guard let count = counts[state], count > 0 else { return nil }
                let glyph = glyphs.glyph(for: state)
                return glyph.isEmpty ? "\(state)\(count)" : "\(glyph)\(count)"
            }.joined(separator: " ")
        }
    }
}

/// Where the companion feed stands.
enum FeedState: Equatable {
    /// Before the first connection attempt has an outcome.
    case connecting
    /// Subscribed; snapshots are arriving.
    case live
    /// Nothing listens on the daemon socket.
    case notRunning
    /// The daemon predates `subscribe`; the app polls `health` instead.
    case daemonTooOld
    /// The stream ended and the client is reconnecting.
    case reconnecting(String)
}

/// The app's view of the live sessions: the latest companion snapshot and the
/// state of the feed that delivers it. Every mutation happens on the main
/// actor, and `onChange` tells the status item to redraw.
@MainActor
final class SessionStore {
    private(set) var feed: FeedState = .connecting
    private(set) var snapshot: CompanionSnapshot?
    /// The last health result, which is what the daemon line shows whenever
    /// the feed is not live.
    private(set) var health: DaemonStatus?
    /// A one-line notice the menu shows until the next snapshot replaces it.
    private(set) var notice: String?
    /// Windows a focus attempt found already closed, hidden until a snapshot
    /// that no longer lists them arrives.
    private(set) var staleWindows: Set<Int> = []

    var onChange: (() -> Void)?

    init() {}

    /// Feeds one subscription event in.
    func apply(_ event: SubscriptionClient.Event) {
        switch event {
        case .connecting:
            return
        case .subscribed:
            feed = .live
        case let .frame(frame):
            guard case let .snapshot(_, snapshot) = frame else { return }
            apply(snapshot: snapshot)
            return
        case let .disconnected(reason, _):
            switch reason {
            case .notRunning:
                feed = .notRunning
                snapshot = nil
            case .unsupported:
                feed = .daemonTooOld
                snapshot = nil
            case let .refused(message):
                feed = .reconnecting(message)
            case let .bye(reason):
                feed = .reconnecting(reason.isEmpty ? "daemon closed the stream" : "daemon \(reason)")
            case .heartbeatLost:
                feed = .reconnecting("no heartbeat")
            case let .lost(message):
                feed = .reconnecting(message)
            case .reconnectRequested:
                feed = .reconnecting("reconnecting")
            }
        }
        onChange?()
    }

    func apply(snapshot: CompanionSnapshot) {
        feed = .live
        self.snapshot = snapshot
        let listed = Set(snapshot.sessions.map(\.windowID))
        staleWindows.formIntersection(listed)
        notice = nil
        onChange?()
    }

    func apply(health: DaemonStatus) {
        self.health = health
        if health == .notRunning, feed != .daemonTooOld {
            feed = .notRunning
            snapshot = nil
        }
        onChange?()
    }

    /// Records that focusing `windowID` found it gone.
    func markStale(windowID: Int) {
        staleWindows.insert(windowID)
        notice = "That session has closed."
        onChange?()
    }

    func show(notice: String?) {
        self.notice = notice
        onChange?()
    }

    /// The daemon status the daemon line shows: the snapshot's own view while
    /// the feed is live, else the last health result.
    var daemonStatus: DaemonStatus? {
        if feed == .live, let snapshot {
            return snapshot.daemon.status
        }
        if feed == .notRunning { return .notRunning }
        return health
    }

    /// The sessions the menu lists.
    var sessions: [CompanionSession] {
        (snapshot?.sessions ?? []).filter { !staleWindows.contains($0.windowID) }
    }

    func title(style: StatusTitleStyle = .glyphs, showWorking: Bool = true, glyphs: Glyphs = .bundled) -> String {
        guard feed == .live, let snapshot else { return "" }
        return StatusTitle.text(for: snapshot.attention, style: style, showWorking: showWorking, glyphs: glyphs)
    }
}
