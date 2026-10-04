import AppKit

/// What Preferences shows. Read-only for now: which amx the app runs, why that
/// one, and what it can do. Settings arrive with the features they govern.
struct PreferencesSnapshot: Equatable {
    var binary: AMXBinaryResolver.Resolution?
    var binaryProblem: String?
    var binaryVersion: AMXVersion?
    var capabilities: AMXCapabilities?
    var daemonStatus: DaemonStatus?

    var lines: [(String, String)] {
        var rows: [(String, String)] = []
        if let binary {
            rows.append(("amx", binary.url.path))
            rows.append(("Chosen as", Self.describe(binary.source)))
        } else {
            rows.append(("amx", binaryProblem ?? "resolving…"))
        }
        rows.append(("Version", binaryVersion?.line ?? "unknown"))
        rows.append(("Daemon", DaemonStatusLine.text(for: daemonStatus)))
        if let capabilities {
            let missing = [
                capabilities.project ? nil : "project",
                capabilities.openDashboard ? nil : "open-dashboard",
                capabilities.notificationsSnooze ? nil : "notifications snooze",
                capabilities.muteToggleJSON ? nil : "mute-toggle --json",
            ].compactMap { $0 }
            rows.append(("Missing commands", missing.isEmpty ? "none" : missing.joined(separator: ", ")))
        }
        return rows
    }

    static func describe(_ source: AMXBinaryResolver.Source) -> String {
        switch source {
        case .userDefaultsOverride: return "the \(AMXBinaryResolver.overrideDefaultsKey) override"
        case .bundled: return "the copy bundled in the app"
        case .localBin: return "~/.local/bin/amx"
        case .localBinMatchingDaemon: return "~/.local/bin/amx, the build the daemon runs"
        case .newerLocalBin: return "~/.local/bin/amx, newer than the bundled copy"
        case .path: return "the first amx on PATH"
        }
    }
}

/// The Preferences window: a shell for the settings later features add.
final class PreferencesWindowController: NSWindowController {
    private let grid = NSGridView()

    init() {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 560, height: 220),
            styleMask: [.titled, .closable],
            backing: .buffered,
            defer: false
        )
        window.title = "Agent Mux Preferences"
        window.isReleasedWhenClosed = false
        super.init(window: window)

        grid.translatesAutoresizingMaskIntoConstraints = false
        grid.rowSpacing = 8
        grid.columnSpacing = 12
        let content = NSView()
        content.addSubview(grid)
        NSLayoutConstraint.activate([
            grid.topAnchor.constraint(equalTo: content.topAnchor, constant: 20),
            grid.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            grid.trailingAnchor.constraint(lessThanOrEqualTo: content.trailingAnchor, constant: -20),
            grid.bottomAnchor.constraint(lessThanOrEqualTo: content.bottomAnchor, constant: -20),
        ])
        window.contentView = content
        window.center()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("init(coder:) is not used") }

    func update(_ snapshot: PreferencesSnapshot) {
        while grid.numberOfRows > 0 {
            grid.removeRow(at: 0)
        }
        for (label, value) in snapshot.lines {
            let name = NSTextField(labelWithString: label + ":")
            name.alignment = .right
            name.textColor = .secondaryLabelColor
            let detail = NSTextField(wrappingLabelWithString: value)
            detail.isSelectable = true
            detail.preferredMaxLayoutWidth = 400
            grid.addRow(with: [name, detail])
        }
        grid.column(at: 0).xPlacement = .trailing
    }

    func show() {
        // An accessory app is not active by default; bring it forward so the
        // window takes key focus.
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}
