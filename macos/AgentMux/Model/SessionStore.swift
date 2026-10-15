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
    /// A one-line notice the menu shows. A snapshot clears it only once the
    /// menu has been opened on it and `noticeHold` has passed, so a refusal
    /// is never gone before the operator looks.
    private(set) var notice: String?
    private var noticeSince = Date.distantPast
    private var noticeSeen = false
    /// The least time a notice stays, seen or not.
    static let noticeHold: TimeInterval = 30
    /// When the daemon last said goodbye, while that still explains a feed
    /// that cannot reach it.
    private var byeAt: Date?
    /// How long after a `bye` a daemon that is not running is taken to be
    /// restarting: the last snapshot stays, marked reconnecting, rather than
    /// the menu emptying for the second a restart takes.
    static let byeHold: TimeInterval = 5
    /// Windows a focus attempt found already closed, hidden until a snapshot
    /// that no longer lists them arrives. A window ID names a window only
    /// inside the Kitty instance that issued it, so each entry is the
    /// socket and window together: a stale window in one Kitty never hides a
    /// live one sharing its number in another.
    private(set) var staleWindows: Set<AttentionCycler.Key> = []
    /// Mutes the app has just asked for, shown on their rows until a snapshot
    /// agrees or `expectedMuteHold` passes. The daemon publishes a mute within
    /// a second, but a menu reopened before then would show the old state and
    /// invite a second click that undoes the first.
    private(set) var expectedMutes: [AttentionCycler.Key: (muted: Bool, until: Date)] = [:]
    /// How long an expected mute outranks snapshots that do not show it yet.
    static let expectedMuteHold: TimeInterval = 5

    var onChange: (() -> Void)?
    private let now: () -> Date

    init(now: @escaping () -> Date = Date.init) {
        self.now = now
    }

    /// Feeds one subscription event in.
    func apply(_ event: SubscriptionClient.Event) {
        switch event {
        case .connecting:
            return
        case .subscribed:
            feed = .live
            byeAt = nil
        case let .frame(frame):
            guard case let .snapshot(_, snapshot) = frame else { return }
            apply(snapshot: snapshot)
            return
        case let .disconnected(reason, _):
            switch reason {
            case .reconnectRequested:
                // The app asked for a fresh connection (the notification
                // claim changed, a daemon just started); the next is made at
                // once, and whatever the feed was it still is until then.
                return
            case .notRunning:
                declareNotRunning()
            case .unsupported:
                feed = .daemonTooOld
                snapshot = nil
                byeAt = nil
            case .bye:
                byeAt = now()
                feed = .reconnecting(DaemonPhrase.restarting)
            default:
                feed = .reconnecting(Self.phrase(for: reason))
            }
        }
        onChange?()
    }

    /// What a disconnect is shown as while the feed is reconnecting. The
    /// subscription logs the raw transport error; a refusal code becomes a
    /// phrase here.
    static func phrase(for reason: SubscriptionClient.Disconnect) -> String {
        switch reason {
        case .notRunning: return DaemonPhrase.notRunning
        case .unsupported: return "the daemon is too old"
        case let .refused(code): return DaemonPhrase.refusal(code)
        case .bye: return DaemonPhrase.restarting
        case .heartbeatLost: return DaemonPhrase.notAnswering
        case let .lost(phrase): return phrase.isEmpty ? DaemonPhrase.notAnswering : phrase
        case .reconnectRequested: return "reconnecting"
        case .unreadableSnapshot: return DaemonPhrase.unreadableSnapshot
        }
    }

    /// Whether a `bye` recent enough to mean a restart is being held.
    var holdingAfterBye: Bool {
        guard let byeAt else { return false }
        return now().timeIntervalSince(byeAt) < Self.byeHold
    }

    /// Nothing listens: the feed is not running and the sessions go, unless a
    /// recent `bye` says the daemon is only restarting.
    private func declareNotRunning() {
        if holdingAfterBye {
            feed = .reconnecting(DaemonPhrase.restarting)
            return
        }
        byeAt = nil
        feed = .notRunning
        snapshot = nil
    }

    func apply(snapshot: CompanionSnapshot) {
        feed = .live
        byeAt = nil
        self.snapshot = snapshot
        let listed = Set(snapshot.sessions.map { AttentionCycler.key($0, snapshotSocket: snapshot.kitty.socket) })
        staleWindows.formIntersection(listed)
        let current = now()
        expectedMutes = expectedMutes.filter { key, expected in
            guard expected.until > current, listed.contains(key) else { return false }
            let shown = snapshot.sessions.first { AttentionCycler.key($0, snapshotSocket: snapshot.kitty.socket) == key }
            return shown?.muted != expected.muted
        }
        if noticeSeen, now().timeIntervalSince(noticeSince) >= Self.noticeHold {
            notice = nil
        }
        onChange?()
    }

    func apply(health: DaemonStatus) {
        self.health = health
        if health == .notRunning, feed != .daemonTooOld {
            declareNotRunning()
        }
        onChange?()
    }

    /// Records that focusing `windowID` in the Kitty at `socket` found it
    /// gone. An empty socket names the snapshot's own, as it does on a row.
    func markStale(socket: String, windowID: Int) {
        let socket = socket.isEmpty ? snapshot?.kitty.socket ?? "" : socket
        staleWindows.insert(AttentionCycler.Key(socket: socket, windowID: windowID))
        setNotice("That session has closed.")
        onChange?()
    }

    /// Shows `windowID` in the Kitty at `socket` as `muted` right away, ahead
    /// of the snapshot that will confirm it. An empty socket names the
    /// snapshot's own, as it does on a row.
    func expectMute(socket: String, windowID: Int, muted: Bool) {
        let socket = socket.isEmpty ? snapshot?.kitty.socket ?? "" : socket
        expectMute(AttentionCycler.Key(socket: socket, windowID: windowID), muted: muted)
    }

    /// Shows the session `key` names as `muted` right away; the key of an
    /// embedded session is its runtime session (`AttentionCycler.Key.embedded`).
    func expectMute(_ key: AttentionCycler.Key, muted: Bool) {
        expectedMutes[key] = (muted, now().addingTimeInterval(Self.expectedMuteHold))
        onChange?()
    }

    /// Withdraws an expected mute, for a toggle that failed.
    func dropExpectedMute(socket: String, windowID: Int) {
        let socket = socket.isEmpty ? snapshot?.kitty.socket ?? "" : socket
        dropExpectedMute(AttentionCycler.Key(socket: socket, windowID: windowID))
    }

    func dropExpectedMute(_ key: AttentionCycler.Key) {
        guard expectedMutes.removeValue(forKey: key) != nil else { return }
        onChange?()
    }

    func show(notice: String?) {
        setNotice(notice)
        onChange?()
    }

    /// Replaces the notice only while it is still `old`, so a progress line
    /// ("Starting the daemon…") gives way to its outcome without wiping a
    /// newer notice. Returns whether it did.
    @discardableResult
    func replace(notice old: String, with new: String?) -> Bool {
        guard notice == old else { return false }
        show(notice: new)
        return true
    }

    /// The menu is showing the current notice. Redraws nothing: what is
    /// shown does not change, only how long it may stay.
    func noticeWasSeen() {
        if notice != nil { noticeSeen = true }
    }

    private func setNotice(_ text: String?) {
        notice = text
        noticeSince = now()
        noticeSeen = false
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
        guard let snapshot else { return [] }
        let current = now()
        return snapshot.sessions.compactMap { session in
            let key = AttentionCycler.key(session, snapshotSocket: snapshot.kitty.socket)
            if staleWindows.contains(key) { return nil }
            guard let expected = expectedMutes[key], expected.until > current else { return session }
            var session = session
            session.muted = expected.muted
            return session
        }
    }

    /// Whether the daemon is holding notifications back right now; false
    /// while the feed is not live, since nothing is known then.
    var notificationsSnoozed: Bool {
        guard feed == .live, let snapshot else { return false }
        return SnoozeState(snapshot.notification).isSnoozed(now: now())
    }

    func title(style: StatusTitleStyle = .glyphs, showWorking: Bool = true, glyphs: Glyphs = .bundled) -> String {
        guard feed == .live, let snapshot else { return "" }
        return StatusTitle.text(for: snapshot.attention, style: style, showWorking: showWorking, glyphs: glyphs)
    }
}
