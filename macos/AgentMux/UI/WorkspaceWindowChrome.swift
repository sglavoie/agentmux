import AppKit

/// Builds the workspace's fixed AppKit chrome. Event handlers and state stay
/// with WorkspaceWindowController; this type owns only view configuration and
/// constraints, which do not depend on the current session.
@MainActor
enum WorkspaceWindowChrome {
    static func makeSidebarToggle() -> NSButton {
        makeTitlebarToggle(symbol: "sidebar.left", title: "Toggle Sidebar", shortcut: "⌘B")
    }

    static func makeInspectorToggle() -> NSButton {
        makeTitlebarToggle(symbol: "sidebar.right", title: "Toggle Inspector", shortcut: "⌘⇧B")
    }

    private static func makeTitlebarToggle(symbol: String, title: String, shortcut: String) -> NSButton {
        let button = NSButton(title: "", target: nil, action: nil)
        button.image = NSImage(systemSymbolName: symbol, accessibilityDescription: title)?
            .withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: 13, weight: .regular))
        button.imagePosition = .imageOnly
        button.isBordered = false
        button.contentTintColor = .secondaryLabelColor
        button.refusesFirstResponder = true
        button.toolTip = "\(title) (\(shortcut))"
        button.translatesAutoresizingMaskIntoConstraints = false
        return button
    }

    /// A title bar accessory holding one of the toggles.
    static func titlebarAccessory(_ toggle: NSButton, at edge: NSLayoutConstraint.Attribute) -> NSTitlebarAccessoryViewController {
        let holder = NSView(frame: NSRect(x: 0, y: 0, width: 32, height: 28))
        holder.addSubview(toggle)
        NSLayoutConstraint.activate([
            toggle.centerXAnchor.constraint(equalTo: holder.centerXAnchor),
            toggle.centerYAnchor.constraint(equalTo: holder.centerYAnchor),
            toggle.widthAnchor.constraint(equalToConstant: 24),
            toggle.heightAnchor.constraint(equalToConstant: 22),
        ])
        let accessory = NSTitlebarAccessoryViewController()
        accessory.view = holder
        accessory.layoutAttribute = edge
        return accessory
    }

    static func sidebar(
        table: WorkspaceSidebarView,
        feedLabel: NSTextField,
        feedButton: NSButton
    ) -> NSViewController {
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("session"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .sourceList
        table.floatsGroupRows = false
        table.allowsEmptySelection = true
        table.allowsMultipleSelection = false
        table.setAccessibilityLabel("Sessions")

        let scroll = NSScrollView()
        scroll.documentView = table
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false

        feedLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        feedLabel.textColor = .secondaryLabelColor
        feedLabel.usesSingleLineMode = false
        feedLabel.cell?.wraps = true
        feedLabel.maximumNumberOfLines = 2
        feedLabel.lineBreakMode = .byWordWrapping
        feedLabel.cell?.truncatesLastVisibleLine = true
        feedLabel.isHidden = true
        feedLabel.translatesAutoresizingMaskIntoConstraints = false

        feedButton.bezelStyle = .rounded
        feedButton.controlSize = .small
        feedButton.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        feedButton.isHidden = true
        feedButton.translatesAutoresizingMaskIntoConstraints = false

        let stack = NSStackView(views: [scroll, feedLabel, feedButton])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 4
        stack.edgeInsets = NSEdgeInsets(top: 0, left: 0, bottom: 6, right: 0)
        let controller = NSViewController()
        controller.view = stack
        NSLayoutConstraint.activate([
            scroll.widthAnchor.constraint(equalTo: stack.widthAnchor),
            feedLabel.leadingAnchor.constraint(equalTo: stack.leadingAnchor, constant: 10),
            // The notice wraps to the sidebar's width; its full text is in
            // the tooltip set by the controller's feed update.
            feedLabel.trailingAnchor.constraint(equalTo: stack.trailingAnchor, constant: -10),
            feedButton.leadingAnchor.constraint(equalTo: stack.leadingAnchor, constant: 10),
        ])
        return controller
    }

    static func installPaneOverlays(
        in pane: NSView,
        placeholder: WorkspacePlaceholderView,
        linkLabel: WorkspaceLinkLabel,
        scrollbar: WorkspaceScrollbarView,
        findBar: WorkspaceFindBar,
        switcher: WorkspaceSwitcherView
    ) {
        placeholder.frame = pane.bounds
        placeholder.autoresizingMask = [.width, .height]
        pane.addSubview(placeholder)

        // Above terminals and the placeholder, below the Go to… list.
        linkLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        linkLabel.textColor = .secondaryLabelColor
        linkLabel.drawsBackground = true
        linkLabel.backgroundColor = .windowBackgroundColor
        linkLabel.lineBreakMode = .byTruncatingMiddle
        linkLabel.wantsLayer = true
        linkLabel.layer?.cornerRadius = 4
        linkLabel.isHidden = true
        linkLabel.refusesFirstResponder = true
        linkLabel.translatesAutoresizingMaskIntoConstraints = false
        pane.addSubview(linkLabel)

        scrollbar.translatesAutoresizingMaskIntoConstraints = false
        pane.addSubview(scrollbar)
        NSLayoutConstraint.activate([
            scrollbar.topAnchor.constraint(equalTo: pane.topAnchor),
            scrollbar.bottomAnchor.constraint(equalTo: pane.bottomAnchor),
            scrollbar.trailingAnchor.constraint(equalTo: pane.trailingAnchor),
            scrollbar.widthAnchor.constraint(equalToConstant: WorkspaceScrollbarView.width),
        ])
        findBar.isHidden = true
        pane.addSubview(findBar)
        NSLayoutConstraint.activate([
            findBar.topAnchor.constraint(equalTo: pane.topAnchor, constant: 8),
            findBar.trailingAnchor.constraint(equalTo: pane.trailingAnchor, constant: -8 - WorkspaceScrollbarView.width),
            findBar.leadingAnchor.constraint(greaterThanOrEqualTo: pane.leadingAnchor, constant: 8),
            linkLabel.leadingAnchor.constraint(equalTo: pane.leadingAnchor, constant: 6),
            linkLabel.bottomAnchor.constraint(equalTo: pane.bottomAnchor, constant: -6),
            linkLabel.trailingAnchor.constraint(lessThanOrEqualTo: pane.trailingAnchor, constant: -6),
        ])

        pane.addSubview(switcher)
        let switcherWidth = switcher.widthAnchor.constraint(equalToConstant: 520)
        switcherWidth.priority = .defaultHigh
        NSLayoutConstraint.activate([
            switcher.topAnchor.constraint(equalTo: pane.topAnchor, constant: 48),
            switcher.centerXAnchor.constraint(equalTo: pane.centerXAnchor),
            switcher.widthAnchor.constraint(lessThanOrEqualTo: pane.widthAnchor, constant: -32),
            switcher.bottomAnchor.constraint(lessThanOrEqualTo: pane.bottomAnchor, constant: -16),
            switcherWidth,
        ])
    }
}
