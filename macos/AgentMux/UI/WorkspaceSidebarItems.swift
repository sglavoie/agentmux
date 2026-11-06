import AppKit

/// One line of the workspace sidebar. Line identity is separate from its
/// current label and decoration, so a changed state can update a row in place.
enum WorkspaceSidebarItem: Equatable {
    case section(String)
    /// Dashboard and Overview share a line when both labels fit.
    case navigation
    /// The line above the sessions that counts each section's.
    case overview([WorkspaceSectionCount])
    case view(WorkspaceUtilityView)
    case group(WorkspaceGroup)
    case row(WorkspaceRow)

    func isSameLine(as other: Self) -> Bool {
        switch (self, other) {
        case let (.section(a), .section(b)): return a == b
        case (.navigation, .navigation), (.overview, .overview): return true
        case let (.view(a), .view(b)): return a.id == b.id
        case let (.group(a), .group(b)):
            return a.project == b.project && a.pinned == b.pinned && a.needsAttention == b.needsAttention
                && a.stateSection == b.stateSection && a.isSectionHeading == b.isSectionHeading
                && a.isProjectHeading == b.isProjectHeading && a.isNestedState == b.isNestedState
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
            case .navigation, .section, .overview, .group: return false
            }
        }
    }

    /// Whether the session at `index` is listed away from its project, under
    /// Pinned or Needs attention, and so names the project. Read from where
    /// the line is, not from the row: a held line keeps its place after the
    /// row's state that put it there has changed.
    static func isAway(at index: Int, in items: [Self]) -> Bool {
        guard items.indices.contains(index),
              let group = items[..<index].last(where: { if case .group = $0 { return true } else { return false } }),
              case let .group(group) = group
        else { return false }
        return group.pinned || group.needsAttention
    }

    /// A session can move only among its project's sessions; a utility view
    /// beside a session can move only among that session's views.
    static func dropSlots(forItemAt index: Int, in items: [Self]) -> [Int] {
        guard items.indices.contains(index) else { return [] }
        switch items[index] {
        case .navigation, .section, .overview, .group:
            return []
        case let .row(row):
            guard let group = items[..<index].lastIndex(where: { if case .group = $0 { return true } else { return false } }),
                  case let .group(parent) = items[group],
                  !row.needsAttention || parent.isNestedState else { return [] }
            let end = items[(group + 1)...].firstIndex { item in
                switch item {
                case .navigation, .section, .overview, .group: return true
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

    /// Only section headings carry state color. Project headers, session
    /// rows, and their utility terminals retain the neutral sidebar surface.
    static func bands(in items: [Self]) -> [(lines: Range<Int>, band: WorkspaceSidebarBand)] {
        items.enumerated().compactMap { index, item in
            guard case let .group(group) = item else { return nil }
            let band: WorkspaceSidebarBand
            if group.needsAttention { band = .attention }
            else if group.pinned { band = .pinned }
            else if group.isSectionHeading, let state = group.stateSection {
                switch state {
                case .waiting: band = .attention
                case .done: band = .stopped
                case .working: band = .running
                case .idle: band = .idle
                }
            } else { return nil }
            return (index..<index + 1, band)
        }
    }

}

/// State colors are confined to section headings, with native text contrast
/// in both appearances. Profile markers and selection remain independent.
enum WorkspaceSidebarBand: Equatable {
    case attention, pinned, stopped, running, idle

    init(state: String) {
        switch WorkspaceStateSection(iconState: state) {
        case .idle: self = .idle
        case .done: self = .stopped
        case .waiting: self = .attention
        case .working: self = .running
        }
    }

    var color: NSColor {
        let base: NSColor
        switch self {
        case .attention: base = .systemOrange
        case .pinned: base = .systemPurple
        case .stopped: base = .systemGreen
        case .running: base = .systemBlue
        case .idle: base = .secondaryLabelColor
        }
        return NSColor(name: nil) { appearance in
            let dark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            return base.withAlphaComponent(dark ? 0.22 : 0.14)
        }
    }
}
