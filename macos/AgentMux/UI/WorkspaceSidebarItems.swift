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

    /// The lines drawn on a background: a tinted section from its heading
    /// to the next one, and under a project each session with the terminals
    /// beside it, by its state.
    static func bands(in items: [Self]) -> [(lines: Range<Int>, band: WorkspaceSidebarBand)] {
        var bands: [(lines: Range<Int>, band: WorkspaceSidebarBand)] = []
        var open: (start: Int, band: WorkspaceSidebarBand)?
        // Inside a tinted section its own color wins over a session's.
        var inSection = false
        for (index, item) in items.enumerated() {
            let band: WorkspaceSidebarBand?
            switch item {
            case .section:
                band = nil
                inSection = false
            case let .group(group):
                band = group.needsAttention ? .attention : group.pinned ? .pinned : nil
                inSection = band != nil
            case let .row(row) where !inSection:
                band = WorkspaceSidebarBand(state: row.iconState)
            case .view, .row: continue
            }
            if let open { bands.append((open.start..<index, open.band)) }
            open = band.map { (index, $0) }
        }
        if let open { bands.append((open.start..<items.count, open.band)) }
        return bands
    }
}

/// The background a section or a session of the sidebar is set apart by.
/// Each band names its light and its dark hue. Both are bright, so the
/// lines on a band are drawn in the light appearance in either one
/// (`WorkspaceSidebarView.syncBandAppearance`).
enum WorkspaceSidebarBand: Equatable {
    /// Needs attention, and a session waiting on the operator:
    /// #FFF659 light, #F0D510 dark.
    case attention
    /// Pinned: #98E7FF light, #6CCFF6 dark.
    case pinned
    /// A session whose turn ended (✓): #FFC0C0 light, #F4A6A6 dark.
    case stopped
    /// A session at work (⚡, 🤖, 🌀): #EAFFEA light, #A8E6B0 dark.
    case running
    /// A session with no state glyph: #E5E5E5 light, #C7C7CC dark.
    case idle

    /// A section's heading and lines are one block; sessions are set apart
    /// from their neighbours.
    var isSection: Bool { self == .attention || self == .pinned }

    /// What a session's icon state is drawn on under its project.
    init(state: String) {
        switch state {
        case "": self = .idle
        case "stop": self = .stopped
        case "key", "question", "warning", "error": self = .attention
        default: self = .running
        }
    }

    /// The light hue (softened for the two strong section hues, so the
    /// lines on it stay legible) or the dark one.
    var color: NSColor {
        let (light, dark): (UInt32, UInt32)
        switch self {
        case .attention: (light, dark) = (0xFFF659, 0xF0D510)
        case .pinned: (light, dark) = (0x98E7FF, 0x6CCFF6)
        case .stopped: (light, dark) = (0xFFC0C0, 0xF4A6A6)
        case .running: (light, dark) = (0xEAFFEA, 0xA8E6B0)
        case .idle: (light, dark) = (0xE5E5E5, 0xC7C7CC)
        }
        let lightAlpha: CGFloat = isSection ? 0.3 : 1
        return NSColor(name: nil, dynamicProvider: { appearance in
            appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
                ? Self.rgb(dark, alpha: 1) : Self.rgb(light, alpha: lightAlpha)
        })
    }

    private static func rgb(_ hex: UInt32, alpha: CGFloat) -> NSColor {
        NSColor(srgbRed: CGFloat(hex >> 16 & 0xFF) / 255, green: CGFloat(hex >> 8 & 0xFF) / 255,
                blue: CGFloat(hex & 0xFF) / 255, alpha: alpha)
    }
}
