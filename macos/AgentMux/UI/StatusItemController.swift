import AppKit

/// What the status item asks of the app.
protocol StatusItemDelegate: AnyObject {
    /// The last health result; nil before the first one arrives.
    var daemonStatus: DaemonStatus? { get }
    /// Asks for a fresh health request; the answer arrives through `refresh()`.
    func refreshDaemonStatus()
    func showPreferences(_ sender: Any?)
}

/// The menu bar icon and its menu. Any click opens the menu; opening it asks
/// the daemon for its health, so the line is current a moment later even when
/// the periodic refresh has not run.
final class StatusItemController: NSObject, NSMenuDelegate {
    let statusItem: NSStatusItem
    let menu = NSMenu()
    private weak var delegate: StatusItemDelegate?

    let daemonItem = NSMenuItem(title: DaemonStatusLine.title(for: nil), action: nil, keyEquivalent: "")
    private let preferencesItem = NSMenuItem(title: "Preferences…", action: #selector(preferences(_:)), keyEquivalent: ",")
    private let quitItem = NSMenuItem(title: "Quit Agent Mux", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")

    init(delegate: StatusItemDelegate, statusBar: NSStatusBar = .system) {
        self.delegate = delegate
        statusItem = statusBar.statusItem(withLength: NSStatusItem.variableLength)
        super.init()

        daemonItem.isEnabled = false
        preferencesItem.target = self
        menu.addItem(daemonItem)
        menu.addItem(.separator())
        menu.addItem(preferencesItem)
        menu.addItem(.separator())
        menu.addItem(quitItem)
        menu.delegate = self
        menu.autoenablesItems = false
        statusItem.menu = menu

        if let button = statusItem.button {
            button.image = Self.icon()
            button.imagePosition = .imageLeading
            button.toolTip = "Agent Mux"
        }
    }

    deinit {
        statusItem.statusBar?.removeStatusItem(statusItem)
    }

    /// The StatusIcon asset as a template image, so the menu bar tints it for
    /// light and dark. `make-icons.swift` draws it.
    static func icon() -> NSImage {
        // A copy, since resizing the shared named image would leak to others.
        let image = (NSImage(named: "StatusIcon")?.copy() as? NSImage)
            ?? NSImage(systemSymbolName: "rectangle.stack", accessibilityDescription: "Agent Mux")
            ?? NSImage(size: NSSize(width: 18, height: 18))
        image.size = NSSize(width: 18, height: 18)
        image.isTemplate = true
        image.accessibilityDescription = "Agent Mux"
        return image
    }

    /// Brings the menu in line with the app's last observations.
    func refresh() {
        let status = delegate?.daemonStatus
        daemonItem.title = DaemonStatusLine.title(for: status)
        statusItem.button?.toolTip = "Agent Mux — daemon \(DaemonStatusLine.text(for: status))"
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        refresh()
        delegate?.refreshDaemonStatus()
    }

    @objc private func preferences(_ sender: Any?) { delegate?.showPreferences(sender) }
}
