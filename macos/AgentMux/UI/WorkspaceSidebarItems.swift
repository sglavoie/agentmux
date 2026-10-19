import AppKit

/// One line of the workspace sidebar. Line identity is separate from its
/// current label and decoration, so a changed state can update a row in place.
enum WorkspaceSidebarItem: Equatable {
    case section(String)
    case view(WorkspaceUtilityView)
    case group(WorkspaceGroup)
    case row(WorkspaceRow)

    func isSameLine(as other: Self) -> Bool {
        switch (self, other) {
        case let (.section(a), .section(b)): return a == b
        case let (.view(a), .view(b)): return a.id == b.id
        case let (.group(a), .group(b)):
            return a.project == b.project && a.pinned == b.pinned && a.needsAttention == b.needsAttention
        case let (.row(a), .row(b)): return a.key == b.key && a.name.isEmpty == b.name.isEmpty
        default: return false
        }
    }

    /// The lines that changed in place, or nil when the list must be loaded
    /// again because a line appeared, vanished, or moved.
    static func changedLines(from old: [Self], to new: [Self]) -> IndexSet? {
        guard old.count == new.count, zip(old, new).allSatisfy({ $0.isSameLine(as: $1) }) else { return nil }
        return IndexSet(old.indices.filter { old[$0] != new[$0] })
    }

    /// Keep physical targets steady during hover or a menu interaction while
    /// refreshing each target's latest values.
    static func holding(_ old: [Self], latest: [Self]) -> [Self] {
        old.map { previous in latest.first { previous.isSameLine(as: $0) } ?? previous }
    }

    /// Hover may delay reordering, but must show a newly opened target.
    static func hasNewTargets(_ latest: [Self], comparedTo old: [Self]) -> Bool {
        latest.contains { item in
            switch item {
            case let .row(row):
                return !old.contains { if case let .row(previous) = $0 { return previous.key == row.key }; return false }
            case let .view(view):
                return !old.contains { if case let .view(previous) = $0 { return previous.id == view.id }; return false }
            case .section, .group: return false
            }
        }
    }

    /// A session can move only among its project's sessions; a utility view
    /// beside a session can move only among that session's views.
    static func dropSlots(forItemAt index: Int, in items: [Self]) -> [Int] {
        guard items.indices.contains(index) else { return [] }
        switch items[index] {
        case .section, .group:
            return []
        case let .row(row):
            // Attention is a temporary position, not a saved drag order.
            guard !row.needsAttention else { return [] }
            guard let group = items[..<index].lastIndex(where: { if case .group = $0 { return true } else { return false } })
            else { return [] }
            let end = items[(group + 1)...].firstIndex { item in
                switch item {
                case .section, .group: return true
                case .view, .row: return false
                }
            } ?? items.count
            return (group + 1 ..< end).filter { if case .row = items[$0] { return true } else { return false } } + [end]
        case let .view(view):
            guard view.besideRuntimeSessionID != nil,
                  let session = items[..<index].lastIndex(where: { if case .view = $0 { return false } else { return true } }),
                  case let .row(row) = items[session], row.embeddedRuntimeSessionID == view.besideRuntimeSessionID
            else { return [] }
            let end = items[(session + 1)...].firstIndex { if case .view = $0 { return false } else { return true } } ?? items.count
            return Array(session + 1 ... end)
        }
    }

    /// The first allowed slot at or below the pointer, excluding a drop that
    /// would leave the line where it already is.
    static func dropSlot(forItemAt index: Int, proposed: Int, in items: [Self]) -> Int? {
        let slots = dropSlots(forItemAt: index, in: items)
        guard let first = slots.first, proposed >= first, let slot = slots.first(where: { $0 >= proposed }) else { return nil }
        let after = slots.first { $0 > index }
        return slot == index || slot == after ? nil : slot
    }
}
