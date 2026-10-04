import AppKit

/// One session row as the menu shows it.
struct MenuSessionRow: Equatable {
    let title: String
    let windowID: Int
    let kittySocket: String
    let toolTip: String
}

/// Something a menu item does besides focusing a session.
enum MenuAction: Equatable {
    case startDaemon
}

/// The menu as data, built from the store so it can be tested without AppKit.
enum MenuEntry: Equatable {
    /// A disabled line of text.
    case info(String)
    case header(String)
    case session(MenuSessionRow)
    case submenu(String, [MenuSessionRow])
    case action(MenuAction, String)
    case separator
}

/// Builds the session part of the status menu: the daemon line, any notice,
/// then the Needs attention, Working, and Idle sections. The controller adds
/// its fixed items (Preferences, Quit) after these.
enum MenuBuilder {
    /// More idle rows than this collapse into a submenu.
    static let idleInlineLimit = 10

    struct Input {
        var feed: FeedState
        var daemonStatus: DaemonStatus?
        var sessions: [CompanionSession]
        var snapshot: CompanionSnapshot?
        var notice: String?
        /// Whether any Kitty socket is known to try; false means `listen_on`
        /// is missing everywhere.
        var kittyReachable = true
        var now = Date()
        var glyphs: Glyphs = .bundled
    }

    static func entries(_ input: Input) -> [MenuEntry] {
        var entries: [MenuEntry] = []
        switch input.feed {
        case .live:
            entries.append(.info(DaemonStatusLine.title(for: input.daemonStatus)))
        case .connecting:
            entries.append(.info("Daemon: connecting…"))
        case .notRunning:
            entries.append(.info(DaemonStatusLine.title(for: .notRunning)))
            entries.append(.action(.startDaemon, "Start daemon"))
        case .daemonTooOld:
            entries.append(.info(DaemonStatusLine.title(for: input.daemonStatus)))
            entries.append(.info("daemon too old — run `amx daemon restart`"))
        case let .reconnecting(reason):
            entries.append(.info("Daemon: reconnecting · \(reason)"))
        }
        if let notice = input.notice, !notice.isEmpty {
            entries.append(.info(notice))
        }
        guard input.feed == .live, input.snapshot != nil else { return entries }

        if !input.sessions.isEmpty, !input.kittyReachable {
            entries.append(.info(KittyEndpoint.listenOnMissing))
        }

        let attention = input.sessions.filter(\.attention).sorted { lhs, rhs in
            (lhs.changedAt ?? .distantFuture) < (rhs.changedAt ?? .distantFuture)
        }
        let working = input.sessions.filter { !$0.attention && $0.isWorking }
        let idle = input.sessions.filter { !$0.attention && !$0.isWorking }

        if input.sessions.isEmpty {
            entries.append(.separator)
            entries.append(.info("No live sessions"))
            return entries
        }
        if !attention.isEmpty {
            entries.append(.separator)
            entries.append(.header("Needs attention"))
            entries += attention.map { .session(row($0, showReason: true, input: input)) }
        }
        if !working.isEmpty {
            entries.append(.separator)
            entries.append(.header("Working"))
            entries += working.map { .session(row($0, showReason: false, input: input)) }
        }
        if !idle.isEmpty {
            entries.append(.separator)
            let rows = idle.map { row($0, showReason: false, input: input) }
            if rows.count > idleInlineLimit {
                entries.append(.submenu("Idle (\(rows.count))", rows))
            } else {
                entries.append(.header("Idle"))
                entries += rows.map { .session($0) }
            }
        }
        if input.snapshot?.truncated == true {
            entries.append(.info("More sessions than the daemon lists"))
        }
        return entries
    }

    /// `🔑 api · claude · 4m · permission`.
    static func row(_ session: CompanionSession, showReason: Bool, input: Input) -> MenuSessionRow {
        var parts: [String] = [name(of: session)]
        if !session.harness.isEmpty { parts.append(session.harness) }
        if let changed = session.changedAt { parts.append(age(since: changed, now: input.now)) }
        if showReason, !session.reason.isEmpty { parts.append(session.reason) }
        var title = parts.joined(separator: " · ")
        let glyph = input.glyphs.glyph(for: session.iconState)
        if !glyph.isEmpty { title = "\(glyph) \(title)" }
        if session.muted { title += " \(input.glyphs.mute)" }

        var tip = ["window \(session.windowID)"]
        if !session.profile.isEmpty { tip.append("profile \(session.profile)") }
        if !session.branch.isEmpty { tip.append("branch \(session.branch)\(session.dirty ? "*" : "")") }
        if (input.snapshot?.kitty.sockets.count ?? 0) > 1, !session.kittySocket.isEmpty {
            tip.append(session.kittySocket)
        }
        return MenuSessionRow(
            title: title,
            windowID: session.windowID,
            kittySocket: session.kittySocket,
            toolTip: tip.joined(separator: " · ")
        )
    }

    static func name(of session: CompanionSession) -> String {
        if !session.project.isEmpty { return session.project }
        if !session.baseTitle.isEmpty { return session.baseTitle }
        return "window \(session.windowID)"
    }

    /// `<1m`, `4m`, `2h`, `3d`.
    static func age(since date: Date, now: Date) -> String {
        let seconds = max(0, Int(now.timeIntervalSince(date)))
        switch seconds {
        case ..<60: return "<1m"
        case ..<3600: return "\(seconds / 60)m"
        case ..<86400: return "\(seconds / 3600)h"
        default: return "\(seconds / 86400)d"
        }
    }

    // MARK: AppKit

    /// Boxes a row for `NSMenuItem.representedObject`.
    final class RowBox: NSObject {
        let row: MenuSessionRow
        init(_ row: MenuSessionRow) { self.row = row }
    }

    /// The menu items for `entries`. Session items send `sessionAction` and
    /// carry their row in `representedObject`; action items send
    /// `actionSelector` with their `MenuAction` tag.
    static func items(
        for entries: [MenuEntry],
        target: AnyObject,
        sessionAction: Selector,
        actionSelector: Selector
    ) -> [NSMenuItem] {
        func sessionItem(_ row: MenuSessionRow) -> NSMenuItem {
            let item = NSMenuItem(title: row.title, action: sessionAction, keyEquivalent: "")
            item.target = target
            item.toolTip = row.toolTip
            item.representedObject = RowBox(row)
            return item
        }
        return entries.map { entry in
            switch entry {
            case let .info(text):
                let item = NSMenuItem(title: text, action: nil, keyEquivalent: "")
                item.isEnabled = false
                return item
            case let .header(title):
                return NSMenuItem.sectionHeader(title: title)
            case let .session(row):
                return sessionItem(row)
            case let .submenu(title, rows):
                let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
                let submenu = NSMenu(title: title)
                submenu.autoenablesItems = false
                rows.forEach { submenu.addItem(sessionItem($0)) }
                item.submenu = submenu
                return item
            case let .action(action, title):
                let item = NSMenuItem(title: title, action: actionSelector, keyEquivalent: "")
                item.target = target
                item.tag = action.tag
                return item
            case .separator:
                return .separator()
            }
        }
    }
}

extension MenuAction {
    var tag: Int {
        switch self {
        case .startDaemon: return 1
        }
    }

    init?(tag: Int) {
        switch tag {
        case 1: self = .startDaemon
        default: return nil
        }
    }
}
