import AppKit

/// What the status item asks of the app.
@MainActor
protocol StatusItemDelegate: AnyObject {
    /// The live sessions and the feed that delivers them.
    var store: SessionStore { get }
    /// Asks for a fresh health request when the feed is not live; the answer
    /// arrives through the store.
    func refreshDaemonStatus()
    /// Focuses the session a menu row names and brings its Kitty forward.
    func focus(_ row: MenuSessionRow)
    func perform(_ action: MenuAction)
    /// Toggles the mute on the session a row names (its ⌥ alternate).
    func toggleMute(_ row: MenuSessionRow)
    func showPreferences(_ sender: Any?)
    /// What the action items and submenus show right now.
    var actionsInput: MenuBuilder.ActionsInput { get }
    /// The menu is opening: refresh whatever lists are stale.
    func menuWillOpen()
}

/// The menu bar icon, its attention counts, and its menu. Any click opens the
/// menu, which is rebuilt from the store each time it opens and again on any
/// store change while it stays open; the title is redrawn on every store
/// change, and the icon is dimmed whenever the feed is not live.
@MainActor
final class StatusItemController: NSObject, NSMenuDelegate {
    let statusItem: NSStatusItem
    let menu = NSMenu()
    private weak var delegate: StatusItemDelegate?
    /// How the title summarizes the counts; Preferences sets these.
    var titleStyle: StatusTitleStyle = .glyphs { didSet { refresh() } }
    var showWorking = true { didSet { refresh() } }
    /// A Kitty socket Preferences names, tried before any other.
    var kittySocketOverride = ""

    /// The action submenus, kept across rebuilds so a list that arrives while
    /// the menu is open fills in where it is.
    private(set) var submenus: [MenuSubmenu: NSMenu] = [:]
    /// Whether the main menu is open, so a store change rebuilds it in place.
    private(set) var isMenuOpen = false
    /// The entries the main menu was last built from.
    private var builtEntries: [MenuEntry]?

    private let preferencesItem = NSMenuItem(title: "Settings…", action: #selector(preferences(_:)), keyEquivalent: ",")
    private let quitItem = NSMenuItem(title: "Quit Agent Mux", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")

    init(delegate: StatusItemDelegate, statusBar: NSStatusBar = .system) {
        self.delegate = delegate
        statusItem = statusBar.statusItem(withLength: NSStatusItem.variableLength)
        super.init()

        preferencesItem.target = self
        menu.delegate = self
        menu.autoenablesItems = false
        for kind in MenuSubmenu.allCases {
            let submenu = NSMenu()
            submenu.autoenablesItems = false
            submenu.delegate = self
            submenus[kind] = submenu
        }
        statusItem.menu = menu
        rebuildMenu()

        if let button = statusItem.button {
            button.image = Self.icon()
            button.imagePosition = .imageOnly
            button.toolTip = "Agent Mux"
        }
    }

    /// The StatusIcon asset as a template image, so the menu bar tints it for
    /// light and dark. `make-icons.swift` draws it.
    nonisolated static func icon() -> NSImage {
        // A copy, since resizing the shared named image would leak to others.
        let image = (NSImage(named: "StatusIcon")?.copy() as? NSImage)
            ?? NSImage(systemSymbolName: "rectangle.stack", accessibilityDescription: "Agent Mux")
            ?? NSImage(size: NSSize(width: 18, height: 18))
        image.size = NSSize(width: 18, height: 18)
        image.isTemplate = true
        image.accessibilityDescription = "Agent Mux"
        return image
    }

    /// The daemon line's text, for the tooltip and tests.
    var daemonLine: String {
        guard let store = delegate?.store else { return DaemonStatusLine.title(for: nil) }
        return MenuBuilder.entries(menuInput(store)).first.flatMap { entry -> String? in
            if case let .info(text) = entry { return text }
            return nil
        } ?? DaemonStatusLine.title(for: store.daemonStatus)
    }

    /// Brings the title and tooltip in line with the store.
    func refresh() {
        guard let store = delegate?.store, let button = statusItem.button else { return }
        let title = store.title(style: titleStyle, showWorking: showWorking)
        button.title = title
        button.imagePosition = title.isEmpty ? .imageOnly : .imageLeading
        button.toolTip = "Agent Mux — \(daemonLine)"
        button.appearsDisabled = Self.appearsDisabled(store.feed)
        if isMenuOpen { rebuildMenuIfChanged() }
    }

    /// Whether the icon is dimmed: whenever the feed is not live, so a down
    /// daemon is visible without opening the menu.
    nonisolated static func appearsDisabled(_ feed: FeedState) -> Bool {
        feed != .live
    }

    /// The main menu's entries as the store and actions stand now.
    private func currentEntries() -> [MenuEntry]? {
        guard let store = delegate?.store, let actions = delegate?.actionsInput else { return nil }
        return MenuBuilder.entries(menuInput(store)) + MenuBuilder.actionEntries(actions)
    }

    /// Rebuilds the open menu when what it would show has changed; a snapshot
    /// that changes nothing visible leaves the items (and the highlight) be.
    func rebuildMenuIfChanged() {
        guard currentEntries() != builtEntries else {
            refreshSubmenus()
            return
        }
        rebuildMenu()
    }

    /// Whether focusing these sessions needs a Kitty socket nothing has named
    /// yet. Only a Kitty session needs one; an embedded one is shown here.
    nonisolated static func needsKittySocket(_ sessions: [CompanionSession], snapshotSocket: String) -> Bool {
        let inKitty = sessions.filter { $0.embeddedRuntimeSessionID == nil }
        return !inKitty.isEmpty && snapshotSocket.isEmpty && inKitty.allSatisfy { $0.kittySocket.isEmpty }
    }

    func menuInput(_ store: SessionStore) -> MenuBuilder.Input {
        let sessions = store.sessions
        let snapshotSocket = store.snapshot?.kitty.socket ?? ""
        var reachable = true
        if Self.needsKittySocket(sessions, snapshotSocket: snapshotSocket) {
            reachable = !KittyEndpoint.candidates(
                override: kittySocketOverride, rowSocket: "", snapshotSocket: "", kittyPIDs: KittyEndpoint.runningKittyPIDs()
            ).isEmpty
        }
        return MenuBuilder.Input(
            feed: store.feed,
            daemonStatus: store.daemonStatus,
            sessions: sessions,
            snapshot: store.snapshot,
            notice: store.notice,
            kittyReachable: reachable,
            gate: delegate?.actionsInput.gate ?? ActionGate()
        )
    }

    func rebuildMenu() {
        // Detach the live submenus from the items about to be dropped, since
        // a menu can belong to only one item.
        for item in menu.items where item.submenu.map({ sub in submenus.values.contains { $0 === sub } }) == true {
            item.submenu = nil
        }
        menu.removeAllItems()
        builtEntries = currentEntries()
        if let entries = builtEntries, let actions = delegate?.actionsInput {
            let items = MenuBuilder.items(
                for: entries,
                target: self,
                sessionAction: #selector(sessionClicked(_:)),
                actionSelector: #selector(actionClicked(_:)),
                muteAction: #selector(muteClicked(_:)),
                submenus: submenus
            )
            Self.showHotKeys(on: items, actions)
            items.forEach(menu.addItem)
            refreshSubmenus(actions)
        }
        menu.addItem(.separator())
        menu.addItem(preferencesItem)
        menu.addItem(.separator())
        menu.addItem(quitItem)
        if isMenuOpen { delegate?.store.noticeWasSeen() }
    }

    /// Shows the global shortcut as the key equivalent of the item it does
    /// the same as, where a menu can spell it.
    static func showHotKeys(on items: [NSMenuItem], _ actions: MenuBuilder.ActionsInput) {
        guard let key = actions.dashboardHotKey?.menuKeyEquivalent else { return }
        for item in items where (item.representedObject as? MenuBuilder.ActionBox)?.action == .openDashboard {
            item.keyEquivalent = key.key
            item.keyEquivalentModifierMask = key.modifiers
        }
    }

    /// Refills the action submenus; safe while one is open.
    func refreshSubmenus(_ actions: MenuBuilder.ActionsInput? = nil) {
        guard let actions = actions ?? delegate?.actionsInput else { return }
        for (kind, submenu) in submenus {
            let items = MenuBuilder.items(
                for: MenuBuilder.entries(for: kind, actions),
                target: self,
                sessionAction: #selector(sessionClicked(_:)),
                actionSelector: #selector(actionClicked(_:))
            )
            submenu.items = items
        }
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        guard menu === self.menu else {
            refreshSubmenus()
            return
        }
        delegate?.menuWillOpen()
        rebuildMenu()
        delegate?.refreshDaemonStatus()
    }

    func menuWillOpen(_ menu: NSMenu) {
        guard menu === self.menu else { return }
        isMenuOpen = true
        delegate?.store.noticeWasSeen()
    }

    func menuDidClose(_ menu: NSMenu) {
        guard menu === self.menu else { return }
        isMenuOpen = false
    }

    @objc private func sessionClicked(_ sender: NSMenuItem) {
        guard let box = sender.representedObject as? MenuBuilder.RowBox else { return }
        delegate?.focus(box.row)
    }

    @objc private func actionClicked(_ sender: NSMenuItem) {
        guard let box = sender.representedObject as? MenuBuilder.ActionBox else { return }
        delegate?.perform(box.action)
    }

    @objc private func muteClicked(_ sender: NSMenuItem) {
        guard let box = sender.representedObject as? MenuBuilder.RowBox else { return }
        delegate?.toggleMute(box.row)
    }

    @objc private func preferences(_ sender: Any?) { delegate?.showPreferences(sender) }
}
