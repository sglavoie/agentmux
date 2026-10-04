import AppKit

/// One session row as the menu shows it.
struct MenuSessionRow: Equatable {
    let title: String
    let windowID: Int
    let kittySocket: String
    let toolTip: String
    /// Whether the session is muted, which names its ⌥ alternate.
    var muted = false
    /// Why the ⌥ Mute alternate is greyed out; nil when it works.
    var muteUnavailable: String?
}

/// Something a menu item does besides focusing a session.
enum MenuAction: Equatable {
    case startDaemon
    case openDashboard
    case launchProject(ProjectEntry)
    case continueConversation(RecentConversation)
    case snooze(SnoozeChoice)
    /// Opens the answer panel on an ASL run waiting for its supervisor.
    case answerASL(ASLAnswerTarget)
}

/// The action submenus the controller keeps as live `NSMenu`s, so a list that
/// arrives while the menu is open fills in where it is.
enum MenuSubmenu: CaseIterable, Equatable {
    case projects
    case recent
    case notifications
}

/// The menu as data, built from the store so it can be tested without AppKit.
enum MenuEntry: Equatable {
    /// A disabled line of text.
    case info(String)
    case header(String)
    case session(MenuSessionRow)
    case submenu(String, [MenuSessionRow])
    case action(MenuAction, String)
    /// An action this amx or daemon cannot run, greyed out with the reason as
    /// its tooltip.
    case unavailable(String, reason: String)
    /// One of the controller's live submenus; greyed out when `reason` is set.
    case submenuRef(MenuSubmenu, String, reason: String?)
    /// "Answer…" for an ASL run waiting on its supervisor; indented under the
    /// session row it belongs to.
    case answer(ASLAnswerTarget, String, indented: Bool)
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
        /// What the resolved amx can do, for the per-row Mute alternates.
        var gate = ActionGate()
    }

    /// What the action items and their submenus are built from.
    struct ActionsInput: Equatable {
        var gate = ActionGate()
        var feedLive = false
        var projects = FetchState<[ProjectEntry]>()
        var recent = FetchState<[RecentConversation]>()
        var recentDays = 7
        var recentLimit = 15
        /// The conversations live sessions report, so Recent can mark them.
        var liveConversations: [ConversationIdentity] = []
        /// The daemon's notification posture; nil when the feed is not live.
        var notification: CompanionNotification?
        /// False once the daemon has answered that it has no snooze.
        var snoozeSupported = true
        var configPath = ""
        var now = Date()
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

        let waiting = waitingBySession(input.snapshot?.asl.waiting ?? [], sessions: input.sessions)
        /// A session row, then the Answer… item for its waiting run.
        func rowEntries(_ session: CompanionSession, showReason: Bool) -> [MenuEntry] {
            var out: [MenuEntry] = [.session(row(session, showReason: showReason, input: input))]
            if let run = waiting.bySession[session.windowID] {
                out.append(.answer(target(run), answerTitle(run), indented: true))
            }
            return out
        }

        if input.sessions.isEmpty {
            entries.append(.separator)
            entries.append(.info("No live sessions"))
            entries += unmatchedWaitingEntries(waiting.unmatched, glyphs: input.glyphs)
            return entries
        }
        if !attention.isEmpty {
            entries.append(.separator)
            entries.append(.header("Needs attention"))
            entries += attention.flatMap { rowEntries($0, showReason: true) }
        }
        if !working.isEmpty {
            entries.append(.separator)
            entries.append(.header("Working"))
            entries += working.flatMap { rowEntries($0, showReason: false) }
        }
        if !idle.isEmpty {
            entries.append(.separator)
            let rows = idle.map { row($0, showReason: false, input: input) }
            if rows.count > idleInlineLimit {
                entries.append(.submenu("Idle (\(rows.count))", rows))
                // A collapsed idle row keeps its Answer… in the menu itself.
                entries += idle.compactMap { session in
                    waiting.bySession[session.windowID].map {
                        MenuEntry.answer(target($0), "\(answerTitle($0)) · \(name(of: session))", indented: false)
                    }
                }
            } else {
                entries.append(.header("Idle"))
                entries += idle.flatMap { rowEntries($0, showReason: false) }
            }
        }
        entries += unmatchedWaitingEntries(waiting.unmatched, glyphs: input.glyphs)
        if input.snapshot?.truncated == true {
            entries.append(.info("More sessions than the daemon lists"))
        }
        return entries
    }

    // MARK: ASL questions

    /// The waiting runs keyed by the window ID of the listed session each
    /// belongs to, and the ones no listed session claims. A run binds to a
    /// session that names it; otherwise to the one session with its window ID
    /// that names no run — window IDs repeat across Kitty instances, so an
    /// ambiguous ID binds nothing.
    static func waitingBySession(_ waiting: [CompanionASLWaiting], sessions: [CompanionSession])
        -> (bySession: [Int: CompanionASLWaiting], unmatched: [CompanionASLWaiting]) {
        var bySession: [Int: CompanionASLWaiting] = [:]
        var unmatched: [CompanionASLWaiting] = []
        for run in waiting {
            var owner = sessions.first { !$0.aslRunID.isEmpty && $0.aslRunID == run.runID }
            if owner == nil, run.windowID > 0 {
                let sameWindow = sessions.filter { $0.windowID == run.windowID }
                if sameWindow.count == 1, sameWindow[0].aslRunID.isEmpty { owner = sameWindow[0] }
            }
            if let owner, bySession[owner.windowID] == nil {
                bySession[owner.windowID] = run
            } else {
                unmatched.append(run)
            }
        }
        return (bySession, unmatched)
    }

    static func target(_ run: CompanionASLWaiting) -> ASLAnswerTarget {
        ASLAnswerTarget(runID: run.runID, planDir: run.planDir, windowID: run.windowID)
    }

    /// `Answer…`, or `Answer… (3 questions)`.
    static func answerTitle(_ run: CompanionASLWaiting) -> String {
        run.unanswered > 1 ? "Answer… (\(run.unanswered) questions)" : "Answer…"
    }

    /// Waiting runs no listed session shows, under their own header.
    static func unmatchedWaitingEntries(_ runs: [CompanionASLWaiting], glyphs: Glyphs) -> [MenuEntry] {
        guard !runs.isEmpty else { return [] }
        var entries: [MenuEntry] = [.separator, .header("Supervisor questions")]
        for run in runs {
            let plan = run.planDir.isEmpty ? run.runID : (run.planDir as NSString).lastPathComponent
            let glyph = glyphs.glyph(for: "question")
            let name = glyph.isEmpty ? plan : "\(glyph) \(plan)"
            entries.append(.answer(target(run), "\(name) · \(answerTitle(run))", indented: false))
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
            toolTip: tip.joined(separator: " · "),
            muted: session.muted,
            muteUnavailable: input.gate.unavailableReason(.muteToggle)
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

    // MARK: Actions

    /// The action items after the sessions: Open Dashboard, then the Launch
    /// Project, Recent, and Notifications submenus.
    static func actionEntries(_ input: ActionsInput) -> [MenuEntry] {
        var entries: [MenuEntry] = [.separator]
        if let reason = input.gate.unavailableReason(.openDashboard) {
            entries.append(.unavailable("Open Dashboard", reason: reason))
        } else {
            entries.append(.action(.openDashboard, "Open Dashboard"))
        }
        entries.append(.submenuRef(.projects, "Launch Project", reason: input.gate.unavailableReason(.project)))
        entries.append(.submenuRef(.recent, "Recent", reason: input.gate.unavailableReason(.sessions)))
        let notifications: String?
        if !input.feedLive {
            notifications = "The daemon is not connected"
        } else if !input.snoozeSupported {
            notifications = snoozeUnsupported
        } else {
            notifications = nil
        }
        entries.append(.submenuRef(.notifications, "Notifications", reason: notifications))
        return entries
    }

    static let snoozeUnsupported = "daemon too old to snooze — run `amx daemon restart`"

    static func entries(for submenu: MenuSubmenu, _ input: ActionsInput) -> [MenuEntry] {
        switch submenu {
        case .projects: return projectEntries(input.projects)
        case .recent: return recentEntries(input)
        case .notifications: return notificationEntries(input)
        }
    }

    /// Projects under one header per profile.
    static func projectEntries(_ state: FetchState<[ProjectEntry]>) -> [MenuEntry] {
        guard let projects = state.value else {
            if let error = state.error { return [.info("Could not list projects: \(error)")] }
            return [.info("Loading…")]
        }
        var entries: [MenuEntry] = []
        if projects.isEmpty {
            entries.append(.info("No configured projects"))
        }
        for (profile, members) in ProjectCatalog.grouped(projects) {
            entries.append(.header(profile.isEmpty ? "(no profile)" : profile))
            entries += members.map { .action(.launchProject($0), $0.title) }
        }
        if let error = state.error {
            entries.append(.separator)
            entries.append(.info("Last refresh failed: \(error)"))
        }
        return entries
    }

    /// The most recently active conversations, newest first.
    static func recentEntries(_ input: ActionsInput) -> [MenuEntry] {
        let state = input.recent
        guard let conversations = state.value else {
            if let error = state.error { return [.info("Could not list conversations: \(error)")] }
            return [.info("Loading…")]
        }
        var entries: [MenuEntry] = []
        let top = RecentSessions.top(conversations, limit: input.recentLimit)
        if top.isEmpty {
            entries.append(.info(input.recentDays == 1 ? "No conversations in the last day" : "No conversations in the last \(input.recentDays) days"))
        }
        for conversation in top {
            let live = input.liveConversations.contains(conversation.identity)
            entries.append(.action(.continueConversation(conversation), recentTitle(conversation, live: live, now: input.now)))
        }
        if state.loading {
            entries.append(.info("Refreshing…"))
        } else if let error = state.error {
            entries.append(.separator)
            entries.append(.info("Last refresh failed: \(error)"))
        }
        return entries
    }

    /// `api · claude · 3h · fix the flaky retry test`, with `· live` for a
    /// conversation a session is running now.
    static func recentTitle(_ conversation: RecentConversation, live: Bool, now: Date) -> String {
        var parts: [String] = []
        let name = conversation.project.isEmpty
            ? (conversation.cwd as NSString).lastPathComponent
            : conversation.project
        parts.append(name.isEmpty ? conversation.id : name)
        if !conversation.harness.isEmpty { parts.append(conversation.harness) }
        if live {
            parts.append("live")
        } else if let lastAt = conversation.lastAt {
            parts.append(age(since: lastAt, now: now))
        }
        let snippet = conversation.snippet
            .split(whereSeparator: \.isNewline).first.map(String.init)?
            .trimmingCharacters(in: .whitespaces) ?? ""
        if !snippet.isEmpty {
            parts.append(snippet.count > snippetLimit ? String(snippet.prefix(snippetLimit - 1)) + "…" : snippet)
        }
        return parts.joined(separator: " · ")
    }

    static let snippetLimit = 48

    /// The snooze line and choices, then the read-only quiet hours.
    static func notificationEntries(_ input: ActionsInput) -> [MenuEntry] {
        guard let notification = input.notification else {
            return [.info("The daemon is not connected")]
        }
        let snooze = SnoozeState(notification)
        let snoozed = snooze.isSnoozed(now: input.now)
        var entries: [MenuEntry] = [.info(snooze.description(now: input.now))]
        for choice in SnoozeChoice.allCases {
            if choice == .resume, !snoozed {
                entries.append(.unavailable(choice.title, reason: "Notifications are not snoozed"))
            } else if choice == .untilResumed, snooze.indefinite {
                entries.append(.unavailable(choice.title, reason: "Already snoozed until resumed"))
            } else {
                entries.append(.action(.snooze(choice), choice.title))
            }
        }
        entries.append(.separator)
        if notification.quietHours.isEmpty {
            entries.append(.info("Quiet hours: off"))
        } else {
            entries.append(.info("Quiet hours: \(notification.quietHours)\(notification.quietNow ? " (now)" : "")"))
        }
        if !input.configPath.isEmpty {
            entries.append(.info("Set in \(input.configPath)"))
        }
        return entries
    }

    // MARK: AppKit

    /// Boxes a row for `NSMenuItem.representedObject`.
    final class RowBox: NSObject {
        let row: MenuSessionRow
        init(_ row: MenuSessionRow) { self.row = row }
    }

    /// Boxes an action for `NSMenuItem.representedObject`.
    final class ActionBox: NSObject {
        let action: MenuAction
        init(_ action: MenuAction) { self.action = action }
    }

    /// `Mute · 🔑 api · claude · 4m`, shown in place of the row while ⌥ is
    /// held.
    static func muteTitle(for row: MenuSessionRow) -> String {
        "\(row.muted ? "Unmute" : "Mute") · \(row.title)"
    }

    /// The menu items for `entries`. Session items send `sessionAction` and
    /// carry their row in `representedObject`; with a `muteAction`, each is
    /// followed by its ⌥ alternate, which sends that instead. Action items
    /// send `actionSelector` and carry their `MenuAction`. A submenu
    /// reference takes its menu from `submenus`.
    static func items(
        for entries: [MenuEntry],
        target: AnyObject,
        sessionAction: Selector,
        actionSelector: Selector,
        muteAction: Selector? = nil,
        submenus: [MenuSubmenu: NSMenu] = [:]
    ) -> [NSMenuItem] {
        func sessionItems(_ row: MenuSessionRow) -> [NSMenuItem] {
            let item = NSMenuItem(title: row.title, action: sessionAction, keyEquivalent: "")
            item.target = target
            item.toolTip = row.toolTip
            item.representedObject = RowBox(row)
            guard let muteAction else { return [item] }
            // An alternate pairs with the item before it when both share a
            // key equivalent and differ only in modifiers.
            item.keyEquivalentModifierMask = []
            let alternate = NSMenuItem(title: muteTitle(for: row), action: muteAction, keyEquivalent: "")
            alternate.target = target
            alternate.keyEquivalentModifierMask = .option
            alternate.isAlternate = true
            alternate.representedObject = RowBox(row)
            if let reason = row.muteUnavailable {
                alternate.isEnabled = false
                alternate.toolTip = reason
            } else {
                alternate.toolTip = row.toolTip
            }
            return [item, alternate]
        }
        func infoItem(_ text: String) -> NSMenuItem {
            let item = NSMenuItem(title: text, action: nil, keyEquivalent: "")
            item.isEnabled = false
            return item
        }
        return entries.flatMap { entry -> [NSMenuItem] in
            switch entry {
            case let .info(text):
                return [infoItem(text)]
            case let .header(title):
                return [NSMenuItem.sectionHeader(title: title)]
            case let .session(row):
                return sessionItems(row)
            case let .submenu(title, rows):
                let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
                let submenu = NSMenu(title: title)
                submenu.autoenablesItems = false
                rows.flatMap(sessionItems).forEach(submenu.addItem)
                item.submenu = submenu
                return [item]
            case let .action(action, title):
                let item = NSMenuItem(title: title, action: actionSelector, keyEquivalent: "")
                item.target = target
                item.representedObject = ActionBox(action)
                return [item]
            case let .unavailable(title, reason):
                let item = infoItem(title)
                item.toolTip = reason
                return [item]
            case let .answer(answerTarget, title, indented):
                let item = NSMenuItem(title: title, action: actionSelector, keyEquivalent: "")
                item.target = target
                item.representedObject = ActionBox(.answerASL(answerTarget))
                item.indentationLevel = indented ? 1 : 0
                return [item]
            case let .submenuRef(kind, title, reason):
                let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
                if let reason {
                    item.isEnabled = false
                    item.toolTip = reason
                } else if let menu = submenus[kind] {
                    // A live menu belongs to one item at a time: the caller
                    // detaches it from the previous build first.
                    item.submenu = menu
                }
                return [item]
            case .separator:
                return [.separator()]
            }
        }
    }
}
