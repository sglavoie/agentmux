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
    /// Set for a session the workspace embeds: focusing it shows its terminal
    /// there, and it has no Kitty window to name.
    var runtimeSessionID = ""

    /// The row a click on `session` focuses, with nothing to display.
    static func focusing(_ session: CompanionSession, title: String? = nil) -> MenuSessionRow {
        MenuSessionRow(title: title ?? session.project, windowID: session.windowID, kittySocket: session.kittySocket,
                       toolTip: "", runtimeSessionID: session.embeddedRuntimeSessionID ?? "")
    }
}

/// Something a menu item does besides focusing a session.
enum MenuAction: Equatable {
    case startDaemon
    /// `amx daemon restart`, offered when the daemon is too old for the app.
    case restartDaemon
    /// Shows the workspace window, where tmux sessions' terminals are.
    case openWorkspace
    case openDashboard
    case launchSettings
    /// Focuses the next session waiting on a human, as ⌃⌥⌘N does.
    case nextAttention
    case launchProject(ProjectEntry)
    /// Asks for a folder, then launches the project there instead of in its
    /// configured directory: a project item's ⌥ alternate.
    case launchProjectInFolder(ProjectEntry)
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
    /// An action that cannot run right now, greyed out with the reason as its
    /// tooltip; it keeps its action so it keeps the shortcut it shows when
    /// it can, as a macOS menu item does.
    case unavailableAction(MenuAction, String, reason: String)
    /// A project whose directory is missing: greyed out with the reason as
    /// its tooltip, while its ⌥ "in Folder…" alternate still launches it
    /// somewhere else.
    case unavailableProject(ProjectEntry, reason: String)
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
        /// The workspace sidebar's arrangement, which orders Needs attention
        /// the way the next-attention shortcut visits it.
        var order = WorkspaceOrder()
        /// What the daemon start or restart under way is doing; nil when none
        /// is. Start Daemon and Restart Daemon are greyed out meanwhile.
        var daemonBusy: String?
    }

    /// What the action items and their submenus are built from.
    struct ActionsInput: Equatable {
        var gate = ActionGate()
        /// Where Open Dashboard shows it; only Kitty needs `open-dashboard`.
        var dashboardPlace: DashboardPlacePreference = .workspace
        var launchBackend: LaunchBackendPreference = .workspace
        var cliLaunchBackend: String?
        var cliLaunchBackendSource: String?
        /// The global shortcut Open Dashboard shows as its key; nil for none.
        var dashboardHotKey: HotKeyCombo?
        /// The global shortcut Next Waiting Session shows as its key.
        var nextAttentionHotKey: HotKeyCombo?
        var feedLive = false
        /// The sessions waiting on the operator, which Next Waiting Session
        /// visits; it is greyed out when there are none.
        var waiting = 0
        var projects = FetchState<[ProjectEntry]>()
        /// Project directories the last survey found missing on disk.
        var missingProjectDirectories: Set<String> = []
        var recent = FetchState<[RecentConversation]>()
        var recentDays = 7
        var recentLimit = 15
        /// The conversations live sessions report, so Recent can mark them.
        var liveConversations: [ConversationIdentity] = []
        /// The daemon's notification posture; nil when the feed is not live.
        var notification: CompanionNotification?
        /// False once the daemon has answered that it has no snooze.
        var snoozeSupported = true
        /// What the daemon start or restart under way is doing; nil when none
        /// is.
        var daemonBusy: String?
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
            entries.append(daemonAction(.startDaemon, "Start Daemon", busy: input.daemonBusy))
        case .daemonTooOld:
            entries.append(.info(DaemonStatusLine.title(for: input.daemonStatus)))
            entries.append(.info("Daemon too old for this app"))
            entries.append(daemonAction(.restartDaemon, "Restart Daemon", busy: input.daemonBusy))
        case let .reconnecting(reason):
            entries.append(.info("Daemon: reconnecting · \(reason)"))
            if WorkspaceSidebar.daemonAction(input.feed) == .restart {
                entries.append(daemonAction(.restartDaemon, "Restart Daemon", busy: input.daemonBusy))
            }
        }
        if let notice = input.notice, !notice.isEmpty {
            entries.append(.info(notice))
            if notice.contains(restartHint), input.feed != .daemonTooOld,
               WorkspaceSidebar.daemonAction(input.feed) != .restart {
                entries.append(daemonAction(.restartDaemon, "Restart Daemon", busy: input.daemonBusy))
            }
        }
        guard input.feed == .live, input.snapshot != nil else { return entries }

        if !input.sessions.isEmpty, !input.kittyReachable {
            entries.append(.info(KittyEndpoint.listenOnMissing))
        }

        let snapshotSocket = input.snapshot?.kitty.socket ?? ""
        // The order the next-attention shortcut visits them in.
        let attention = AttentionCycler.ordered(input.sessions, snapshotSocket: snapshotSocket, order: input.order)
        let working = input.sessions.filter { !$0.attention && $0.isWorking }
        let idle = input.sessions.filter { !$0.attention && !$0.isWorking }

        let waiting = waitingBySession(input.snapshot?.asl.waiting ?? [], sessions: input.sessions, snapshotSocket: snapshotSocket)
        /// A session row, then the Answer… item for its waiting run.
        func rowEntries(_ session: CompanionSession, showReason: Bool) -> [MenuEntry] {
            var out: [MenuEntry] = [.session(row(session, showReason: showReason, input: input))]
            if let run = waiting.bySession[AttentionCycler.key(session, snapshotSocket: snapshotSocket)] {
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
                    waiting.bySession[AttentionCycler.key(session, snapshotSocket: snapshotSocket)].map {
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

    /// Start Daemon or Restart Daemon, greyed out with what is under way
    /// while a start or restart runs: a second one could stop the daemon the
    /// first has just started.
    static func daemonAction(_ action: MenuAction, _ title: String, busy: String?) -> MenuEntry {
        guard let busy else { return .action(action, title) }
        return .unavailable(title, reason: busy)
    }

    // MARK: ASL questions

    /// The waiting runs keyed by the listed session each belongs to, and the
    /// ones no listed session claims. A run binds to a session that names it,
    /// then to the session its runtime session ID names; otherwise to the one
    /// session with its window ID that names no run — window IDs repeat across
    /// Kitty instances, so an ambiguous ID binds nothing.
    static func waitingBySession(_ waiting: [CompanionASLWaiting], sessions: [CompanionSession], snapshotSocket: String = "")
        -> (bySession: [AttentionCycler.Key: CompanionASLWaiting], unmatched: [CompanionASLWaiting]) {
        var bySession: [AttentionCycler.Key: CompanionASLWaiting] = [:]
        var unmatched: [CompanionASLWaiting] = []
        for run in waiting {
            var owner = sessions.first { !$0.aslRunID.isEmpty && $0.aslRunID == run.runID }
            if owner == nil, !run.runtimeSessionID.isEmpty {
                owner = sessions.first { $0.runtime?.sessionID == run.runtimeSessionID && $0.aslRunID.isEmpty }
            }
            if owner == nil, run.windowID > 0 {
                let sameWindow = sessions.filter { $0.windowID == run.windowID }
                if sameWindow.count == 1, sameWindow[0].aslRunID.isEmpty { owner = sameWindow[0] }
            }
            let key = owner.map { AttentionCycler.key($0, snapshotSocket: snapshotSocket) }
            if let key, bySession[key] == nil {
                bySession[key] = run
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
        if let changed = session.changedAt { parts.append(AgeFormatter.string(since: changed, now: input.now, style: .compact)) }
        if showReason, !session.reason.isEmpty { parts.append(WorkspaceWording.status(session.reason)) }
        var title = parts.joined(separator: " · ")
        let glyph = input.glyphs.glyph(for: session.iconState)
        if !glyph.isEmpty { title = "\(glyph) \(title)" }
        if session.muted { title += " \(input.glyphs.mute)" }

        let embedded = session.embeddedRuntimeSessionID
        var tip = [embedded == nil ? "window \(session.windowID)" : place(of: session)]
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
            muteUnavailable: input.gate.unavailableReason(.muteToggle),
            runtimeSessionID: embedded ?? ""
        )
    }

    static func name(of session: CompanionSession) -> String {
        if !session.project.isEmpty { return session.project }
        if !session.baseTitle.isEmpty { return session.baseTitle }
        return place(of: session)
    }

    /// Where a session runs, in a few words: `window 4`, or `tmux %7`.
    static func place(of session: CompanionSession) -> String {
        guard session.embeddedRuntimeSessionID != nil, let runtime = session.runtime else {
            return "window \(session.windowID)"
        }
        return runtime.pane.isEmpty ? runtime.backend : "\(runtime.backend) \(runtime.pane)"
    }

    // MARK: Actions

    /// The action items after the sessions: Open Workspace, Open Dashboard,
    /// and Next Waiting Session, then the Launch Project, Recent, and
    /// Notifications submenus.
    static func actionEntries(_ input: ActionsInput) -> [MenuEntry] {
        var entries: [MenuEntry] = [.separator, .action(.openWorkspace, "Open Workspace")]
        if input.dashboardPlace == .kitty, let reason = input.gate.unavailableReason(.openDashboard) {
            entries.append(.unavailable("Open Dashboard", reason: reason))
        } else {
            entries.append(.action(.openDashboard, "Open Dashboard"))
        }
        if !input.feedLive {
            entries.append(.unavailableAction(.nextAttention, HotKeyPreference.nextAttention.label, reason: DaemonPhrase.notConnected))
        } else if input.waiting == 0 {
            entries.append(.unavailableAction(.nextAttention, HotKeyPreference.nextAttention.label, reason: "No session is waiting"))
        } else {
            entries.append(.action(.nextAttention, HotKeyPreference.nextAttention.label))
        }
        entries.append(.submenuRef(.projects, "Launch Project", reason: input.gate.unavailableReason(.project)))
        entries.append(.submenuRef(.recent, "Recent", reason: input.gate.unavailableReason(.sessions)))
        // A daemon too old to snooze leaves the submenu open, to say so and
        // offer the Restart Daemon its reason names.
        let notifications = input.feedLive ? nil : DaemonPhrase.notConnected
        entries.append(.submenuRef(.notifications, "Notifications", reason: notifications))
        return entries
    }

    static let snoozeUnsupported = "Daemon too old to snooze — \(restartHint)"
    /// Why a timed snooze is greyed out while a longer one is in force.
    static let snoozeLonger = "Already snoozed for longer"
    /// The end of every notice that sends the operator to Restart Daemon;
    /// wherever one is shown, the menu offers the item it names.
    static let restartHint = "choose Restart Daemon"

    static func entries(for submenu: MenuSubmenu, _ input: ActionsInput) -> [MenuEntry] {
        switch submenu {
        case .projects:
            return [.info("Launch in \(input.launchBackend.destination(cliBackend: input.cliLaunchBackend)) · \(input.launchBackend.source(cliSource: input.cliLaunchBackendSource))"),
                    .action(.launchSettings, "Launch Settings…"), .separator]
                + projectEntries(input.projects, missing: input.missingProjectDirectories)
        case .recent: return recentEntries(input)
        case .notifications: return notificationEntries(input)
        }
    }

    /// The longest a line of text in the menu runs before it is cut.
    static let menuLineLimit = 100

    /// A line of text as a menu item shows it: its first line, cut at a word
    /// within `menuLineLimit` with an ellipsis, and the whole text as the
    /// tooltip when anything was left out. An error from amx can run to
    /// several lines and hundreds of characters, which would otherwise widen
    /// the whole menu.
    static func menuLine(_ text: String) -> (title: String, toolTip: String?) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let first = trimmed.split(whereSeparator: \.isNewline).first.map(String.init) ?? ""
        guard first.count > menuLineLimit || first != trimmed else { return (trimmed, nil) }
        guard first.count > menuLineLimit else { return (first + " …", trimmed) }
        var cut = String(first.prefix(menuLineLimit))
        if let space = cut.lastIndex(of: " "), cut.distance(from: cut.startIndex, to: space) > menuLineLimit / 2 {
            cut = String(cut[..<space])
        }
        return (cut.trimmingCharacters(in: .whitespaces.union(.punctuationCharacters)) + "…", trimmed)
    }

    /// Projects under one header per profile. A project whose directory is
    /// missing stays listed, greyed out, with the directory as the reason.
    static func projectEntries(_ state: FetchState<[ProjectEntry]>, missing: Set<String> = []) -> [MenuEntry] {
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
            entries += members.map { project in
                missing.contains(project.directory)
                    ? .unavailableProject(project, reason: "Directory not found: \(project.directory)")
                    : .action(.launchProject(project), project.title)
            }
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
            parts.append(AgeFormatter.string(since: lastAt, now: now, style: .compact))
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
            return [.info(DaemonPhrase.notConnected)]
        }
        guard input.snoozeSupported else {
            return [.info(snoozeUnsupported), daemonAction(.restartDaemon, "Restart Daemon", busy: input.daemonBusy)]
        }
        let snooze = SnoozeState(notification)
        let snoozed = snooze.isSnoozed(now: input.now)
        var entries: [MenuEntry] = [.info(snooze.description(now: input.now))]
        for choice in SnoozeChoice.allCases {
            let title = choice.title(now: input.now)
            if choice == .resume, !snoozed {
                entries.append(.unavailable(title, reason: SnoozeChoice.notSnoozed))
            } else if choice == .untilResumed, snooze.indefinite {
                entries.append(.unavailable(title, reason: "Already snoozed until resumed"))
            } else if snooze.wouldShorten(to: choice, now: input.now) {
                entries.append(.unavailable(title, reason: Self.snoozeLonger))
            } else {
                entries.append(.action(.snooze(choice), title))
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
    /// send `actionSelector` and carry their `MenuAction`; one that launches
    /// a project is followed by its ⌥ alternate, which asks for a folder
    /// first. A submenu reference takes its menu from `submenus`.
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
            let line = menuLine(text)
            let item = NSMenuItem(title: line.title, action: nil, keyEquivalent: "")
            item.toolTip = line.toolTip
            item.isEnabled = false
            return item
        }
        // A project's ⌥ alternate, which asks for a folder first; enabled
        // even when the project's own directory is missing.
        func inFolderItem(after item: NSMenuItem, _ project: ProjectEntry) -> NSMenuItem {
            item.keyEquivalentModifierMask = []
            let alternate = NSMenuItem(title: "\(item.title) in Folder…", action: actionSelector, keyEquivalent: "")
            alternate.target = target
            alternate.keyEquivalentModifierMask = .option
            alternate.isAlternate = true
            alternate.representedObject = ActionBox(.launchProjectInFolder(project))
            return alternate
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
                guard case let .launchProject(project) = action else { return [item] }
                return [item, inFolderItem(after: item, project)]
            case let .unavailable(title, reason):
                let item = infoItem(title)
                item.toolTip = reason
                return [item]
            case let .unavailableAction(action, title, reason):
                let item = infoItem(title)
                item.toolTip = reason
                item.representedObject = ActionBox(action)
                return [item]
            case let .unavailableProject(project, reason):
                let item = infoItem(project.title)
                item.toolTip = reason
                return [item, inFolderItem(after: item, project)]
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
