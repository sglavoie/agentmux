import Foundation

/// Which session the next-attention shortcut focuses.
///
/// The rows waiting on a human are visited in the order the workspace
/// sidebar lists every session (`WorkspaceSidebar.ordered`): the temporary
/// attention section, then pinned sessions and projects in their saved order. How
/// long a session has waited moves nothing. Each press moves past the session
/// the previous press focused and wraps after the last one.
///
/// A window ID names a window only inside the Kitty instance that issued it,
/// so a Kitty session is identified by its socket and window together and the
/// cycle spans every Kitty instance the snapshot lists. A session on another
/// backend has no window; its runtime session ID alone identifies it.
struct AttentionCycler {
    /// One session across Kitty instances and backends.
    struct Key: Hashable {
        let socket: String
        let windowID: Int
        /// Set, with no socket or window, for a session the workspace embeds.
        var runtimeSessionID = ""

        /// The key of the embedded session `runtimeSessionID` names.
        static func embedded(_ runtimeSessionID: String) -> Key {
            Key(socket: "", windowID: 0, runtimeSessionID: runtimeSessionID)
        }
    }

    /// The session the last press focused, and the sessions listed after it
    /// then, around to the one before it.
    private(set) var last: (key: Key, following: [Key])?

    init() {}

    /// The session a row is, naming the snapshot's socket when the row has
    /// none of its own.
    static func key(_ session: CompanionSession, snapshotSocket: String) -> Key {
        if let embedded = session.embeddedRuntimeSessionID { return .embedded(embedded) }
        return Key(socket: session.kittySocket.isEmpty ? snapshotSocket : session.kittySocket, windowID: session.windowID)
    }

    /// The attention rows in cycle order.
    static func ordered(
        _ sessions: [CompanionSession],
        snapshotSocket: String = "",
        order: WorkspaceOrder = WorkspaceOrder()
    ) -> [CompanionSession] {
        WorkspaceSidebar.ordered(sessions, snapshotSocket: snapshotSocket, order: order).filter(\.attention)
    }

    /// The first waiting session listed after the one the last press focused,
    /// wrapping to the first of all. One that no longer waits (focusing it
    /// often settles it) still has its place, and one that is gone leaves the
    /// places of those that followed it, so the cycle goes on from where it
    /// stood rather than starting over.
    func next(
        in sessions: [CompanionSession],
        snapshotSocket: String = "",
        order: WorkspaceOrder = WorkspaceOrder()
    ) -> CompanionSession? {
        let listed = WorkspaceSidebar.ordered(sessions, snapshotSocket: snapshotSocket, order: order)
        let waiting = listed.filter(\.attention)
        guard let last, !waiting.isEmpty else { return waiting.first }
        let following = Self.following(last.key, in: listed.map { Self.key($0, snapshotSocket: snapshotSocket) }) ?? last.following
        for key in following + [last.key] {
            if let session = waiting.first(where: { Self.key($0, snapshotSocket: snapshotSocket) == key }) { return session }
        }
        return waiting[0]
    }

    /// Records the session a press focused.
    mutating func focused(
        _ session: CompanionSession,
        in sessions: [CompanionSession],
        snapshotSocket: String = "",
        order: WorkspaceOrder = WorkspaceOrder()
    ) {
        let key = Self.key(session, snapshotSocket: snapshotSocket)
        let listed = WorkspaceSidebar.ordered(sessions, snapshotSocket: snapshotSocket, order: order)
        last = (key, Self.following(key, in: listed.map { Self.key($0, snapshotSocket: snapshotSocket) }) ?? [])
    }

    /// Picks the next session and records it, in one step.
    mutating func advance(
        in sessions: [CompanionSession],
        snapshotSocket: String = "",
        order: WorkspaceOrder = WorkspaceOrder()
    ) -> CompanionSession? {
        guard let session = next(in: sessions, snapshotSocket: snapshotSocket, order: order) else { return nil }
        focused(session, in: sessions, snapshotSocket: snapshotSocket, order: order)
        return session
    }

    /// The keys listed after `key`, around to the one before it; nil when
    /// `key` is not listed.
    private static func following(_ key: Key, in keys: [Key]) -> [Key]? {
        guard let index = keys.firstIndex(of: key) else { return nil }
        return Array(keys[(index + 1)...] + keys[..<index])
    }
}
