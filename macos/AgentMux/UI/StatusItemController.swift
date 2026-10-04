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
    func showPreferences(_ sender: Any?)
}

/// The menu bar icon, its attention counts, and its menu. Any click opens the
/// menu, which is rebuilt from the store each time it opens; the title is
/// redrawn on every store change.
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

    private let preferencesItem = NSMenuItem(title: "Preferences…", action: #selector(preferences(_:)), keyEquivalent: ",")
    private let quitItem = NSMenuItem(title: "Quit Agent Mux", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")

    init(delegate: StatusItemDelegate, statusBar: NSStatusBar = .system) {
        self.delegate = delegate
        statusItem = statusBar.statusItem(withLength: NSStatusItem.variableLength)
        super.init()

        preferencesItem.target = self
        menu.delegate = self
        menu.autoenablesItems = false
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
    }

    func menuInput(_ store: SessionStore) -> MenuBuilder.Input {
        let sessions = store.sessions
        let snapshotSocket = store.snapshot?.kitty.socket ?? ""
        var reachable = true
        if !sessions.isEmpty, snapshotSocket.isEmpty, sessions.allSatisfy({ $0.kittySocket.isEmpty }) {
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
            kittyReachable: reachable
        )
    }

    func rebuildMenu() {
        menu.removeAllItems()
        if let store = delegate?.store {
            let items = MenuBuilder.items(
                for: MenuBuilder.entries(menuInput(store)),
                target: self,
                sessionAction: #selector(sessionClicked(_:)),
                actionSelector: #selector(actionClicked(_:))
            )
            items.forEach(menu.addItem)
        }
        menu.addItem(.separator())
        menu.addItem(preferencesItem)
        menu.addItem(.separator())
        menu.addItem(quitItem)
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        rebuildMenu()
        delegate?.refreshDaemonStatus()
    }

    @objc private func sessionClicked(_ sender: NSMenuItem) {
        guard let box = sender.representedObject as? MenuBuilder.RowBox else { return }
        delegate?.focus(box.row)
    }

    @objc private func actionClicked(_ sender: NSMenuItem) {
        guard let action = MenuAction(tag: sender.tag) else { return }
        delegate?.perform(action)
    }

    @objc private func preferences(_ sender: Any?) { delegate?.showPreferences(sender) }
}
