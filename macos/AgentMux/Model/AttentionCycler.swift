import Foundation

/// Which session the next-attention shortcut focuses.
///
/// The rows waiting on a human are visited in the order the workspace
/// sidebar lists every session (`WorkspaceSidebar.ordered`): the temporary
/// attention section, then pinned sessions and projects in their saved order. How
/// long a session has waited moves nothing. Each press moves past the session
/// the previous press focused and wraps after the last one.
///
/// A runtime session ID identifies each row across snapshots.
struct AttentionCycler {
    /// What the command that runs the cycle is called everywhere it is
    /// offered: the status menu, the workspace's Session menu and F4, the
    /// command palette, and the global shortcut in Settings.
    static let commandTitle = "Next Waiting Session"

    /// One runtime session.
    struct Key: Hashable {
        let runtimeSessionID: String

        /// The key of the embedded session `runtimeSessionID` names.
        static func embedded(_ runtimeSessionID: String) -> Key {
            Key(runtimeSessionID: runtimeSessionID)
        }
    }

    /// The session the last press focused, and the sessions listed after it
    /// then, around to the one before it.
    private(set) var last: (key: Key, following: [Key])?

    init() {}

    /// The session a row names.
    static func key(_ session: CompanionSession) -> Key {
        .embedded(session.embeddedRuntimeSessionID ?? "")
    }

    /// The attention rows in cycle order.
    static func ordered(
        _ sessions: [CompanionSession],
        order: WorkspaceOrder = WorkspaceOrder(),
        grouping: WorkspaceSidebarGrouping = .stateThenProject
    ) -> [CompanionSession] {
        WorkspaceSidebar.ordered(sessions, order: order, grouping: grouping).filter(\.attention)
    }

    /// The first waiting session listed after the one the last press focused,
    /// wrapping to the first of all. One that no longer waits (focusing it
    /// often settles it) still has its place, and one that is gone leaves the
    /// places of those that followed it, so the cycle goes on from where it
    /// stood rather than starting over.
    ///
    /// The places are those the sidebar listed at that press. Settling moves
    /// the focused row out of Needs attention into its project, so where it
    /// is listed now would skip the rows that followed it then. Sessions
    /// listed only since come after those, in the order listed now.
    func next(
        in sessions: [CompanionSession],
        order: WorkspaceOrder = WorkspaceOrder(),
        grouping: WorkspaceSidebarGrouping = .stateThenProject
    ) -> CompanionSession? {
        let listed = WorkspaceSidebar.ordered(sessions, order: order, grouping: grouping)
        let waiting = listed.filter(\.attention)
        guard let last, !waiting.isEmpty else { return waiting.first }
        let known = Set(last.following + [last.key])
        let added = listed.map { Self.key($0) }.filter { !known.contains($0) }
        for key in last.following + added + [last.key] {
            if let session = waiting.first(where: { Self.key($0) == key }) { return session }
        }
        return waiting[0]
    }

    /// Records the session a press focused.
    mutating func focused(
        _ session: CompanionSession,
        in sessions: [CompanionSession],
        order: WorkspaceOrder = WorkspaceOrder(),
        grouping: WorkspaceSidebarGrouping = .stateThenProject
    ) {
        let key = Self.key(session)
        let listed = WorkspaceSidebar.ordered(sessions, order: order, grouping: grouping)
        last = (key, Self.following(key, in: listed.map { Self.key($0) }) ?? [])
    }

    /// Picks the next session and records it, in one step.
    mutating func advance(
        in sessions: [CompanionSession],
        order: WorkspaceOrder = WorkspaceOrder(),
        grouping: WorkspaceSidebarGrouping = .stateThenProject
    ) -> CompanionSession? {
        guard let session = next(in: sessions, order: order, grouping: grouping) else { return nil }
        focused(session, in: sessions, order: order, grouping: grouping)
        return session
    }

    /// The keys listed after `key`, around to the one before it; nil when
    /// `key` is not listed.
    private static func following(_ key: Key, in keys: [Key]) -> [Key]? {
        guard let index = keys.firstIndex(of: key) else { return nil }
        return Array(keys[(index + 1)...] + keys[..<index])
    }
}
