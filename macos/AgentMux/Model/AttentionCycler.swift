import Foundation

/// Which session the next-attention shortcut focuses.
///
/// The rows waiting on a human are ordered oldest first, by when their
/// presentation last changed, so the session that has waited longest comes
/// first — the order the menu's Needs attention section uses. Each press moves
/// past the session the previous press focused and wraps after the last one.
///
/// A window ID names a window only inside the Kitty instance that issued it,
/// so a session is identified by its socket and window together and the cycle
/// spans every Kitty instance the snapshot lists.
struct AttentionCycler {
    /// One session across Kitty instances.
    struct Key: Hashable {
        let socket: String
        let windowID: Int
    }

    /// The session the last press focused, and where it stood in the order.
    private(set) var last: (key: Key, position: Position)?

    /// Where a session sorts: oldest change first, then socket and window so
    /// the order is total and stable between snapshots.
    struct Position: Comparable {
        let changedAt: Date
        let socket: String
        let windowID: Int

        static func < (lhs: Position, rhs: Position) -> Bool {
            if lhs.changedAt != rhs.changedAt { return lhs.changedAt < rhs.changedAt }
            if lhs.socket != rhs.socket { return lhs.socket < rhs.socket }
            return lhs.windowID < rhs.windowID
        }
    }

    init() {}

    /// The session a row is, naming the snapshot's socket when the row has
    /// none of its own.
    static func key(_ session: CompanionSession, snapshotSocket: String) -> Key {
        Key(socket: session.kittySocket.isEmpty ? snapshotSocket : session.kittySocket, windowID: session.windowID)
    }

    static func position(_ session: CompanionSession, snapshotSocket: String) -> Position {
        let key = key(session, snapshotSocket: snapshotSocket)
        // A row with no timestamp has no age to rank by; it goes last.
        return Position(changedAt: session.changedAt ?? .distantFuture, socket: key.socket, windowID: key.windowID)
    }

    /// The attention rows in cycle order.
    static func ordered(_ sessions: [CompanionSession], snapshotSocket: String = "") -> [CompanionSession] {
        sessions.filter(\.attention).sorted {
            position($0, snapshotSocket: snapshotSocket) < position($1, snapshotSocket: snapshotSocket)
        }
    }

    /// The session after the one the last press focused, wrapping to the
    /// oldest. When that one no longer waits (focusing it often settles it),
    /// the cycle continues from where it stood rather than starting over.
    func next(in sessions: [CompanionSession], snapshotSocket: String = "") -> CompanionSession? {
        let rows = Self.ordered(sessions, snapshotSocket: snapshotSocket)
        guard !rows.isEmpty else { return nil }
        guard let last else { return rows[0] }
        if let index = rows.firstIndex(where: { Self.key($0, snapshotSocket: snapshotSocket) == last.key }) {
            return rows[(index + 1) % rows.count]
        }
        return rows.first { Self.position($0, snapshotSocket: snapshotSocket) > last.position } ?? rows[0]
    }

    /// Records the session a press focused.
    mutating func focused(_ session: CompanionSession, snapshotSocket: String = "") {
        last = (Self.key(session, snapshotSocket: snapshotSocket), Self.position(session, snapshotSocket: snapshotSocket))
    }

    /// Picks the next session and records it, in one step.
    mutating func advance(in sessions: [CompanionSession], snapshotSocket: String = "") -> CompanionSession? {
        guard let session = next(in: sessions, snapshotSocket: snapshotSocket) else { return nil }
        focused(session, snapshotSocket: snapshotSocket)
        return session
    }
}
