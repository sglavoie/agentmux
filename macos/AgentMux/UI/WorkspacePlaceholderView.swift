import AppKit

/// The active placeholder's primary action takes Return from the pane or
/// sidebar, without taking input from text fields, the inspector, or terminals.
@MainActor
final class WorkspacePlaceholderButton: NSButton {
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard event.type == .keyDown, [36, 76].contains(event.keyCode),
              event.modifierFlags.isDisjoint(with: [.command, .control, .option, .shift]),
              keyEquivalent == "\r", isEnabled, !isHiddenOrHasHiddenAncestor,
              let window, window.attachedSheet == nil,
              window.firstResponder === self || window.firstResponder === window
                || window.firstResponder is WorkspaceSidebarView else { return false }
        performClick(nil)
        return true
    }
}

/// What the pane shows when it has no terminal to show: a title, a line of
/// explanation, a primary action, and optional grouped shortcuts.
@MainActor
final class WorkspacePlaceholderView: NSView {
    static let projectsHeading = "Projects"
    static let recentHeading = "Recent conversations"

    let titleLabel = NSTextField(labelWithString: "")
    let messageLabel = NSTextField(wrappingLabelWithString: "")
    let button = WorkspacePlaceholderButton(title: "", target: nil, action: nil)
    private var onPress: (() -> Void)?
    let secondaryActions = NSStackView()
    private var secondaryHandlers: [() -> Void] = []

    init() {
        super.init(frame: .zero)
        titleLabel.font = .systemFont(ofSize: 17, weight: .semibold)
        messageLabel.textColor = .secondaryLabelColor
        messageLabel.alignment = .center
        button.bezelStyle = .rounded
        button.keyEquivalentModifierMask = []
        button.target = self
        button.action = #selector(pressed(_:))
        secondaryActions.orientation = .vertical
        secondaryActions.spacing = 6
        let stack = NSStackView(views: [titleLabel, messageLabel, button, secondaryActions])
        stack.orientation = .vertical
        stack.spacing = 8
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 420),
            stack.leadingAnchor.constraint(greaterThanOrEqualTo: leadingAnchor, constant: 20),
        ])
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    /// With `isDefault`, Return presses the button, as it does a sheet's.
    func show(title: String, message: String, button label: String? = nil, isDefault: Bool = true, secondary: [(String, () -> Void)] = [], groups: [(heading: String?, actions: [(String, () -> Void)])]? = nil, onPress: (() -> Void)? = nil) {
        if let groups { showGroups(groups) } else { showSecondaryActions(secondary) }
        titleLabel.stringValue = title
        messageLabel.stringValue = message
        button.title = label ?? ""
        button.isHidden = label == nil
        button.keyEquivalent = isDefault && label != nil ? "\r" : ""
        self.onPress = onPress
    }

    func showSecondaryActions(_ actions: [(String, () -> Void)]) {
        showGroups([(heading: nil, actions: actions)])
    }

    func showGroups(_ groups: [(heading: String?, actions: [(String, () -> Void)])]) {
        let groups = groups.filter { !$0.actions.isEmpty }
        secondaryHandlers = groups.flatMap { $0.actions.map { $0.1 } }
        secondaryActions.isHidden = groups.isEmpty
        // Preserve the buttons (and keyboard focus) when only handlers change.
        let signature = groups.flatMap { group in
            (group.heading.map { ["heading:" + $0] } ?? []) + group.actions.map { "button:" + $0.0 }
        }
        if secondaryActions.arrangedSubviews.map({ view in
            if let button = view as? NSButton { return "button:" + button.title }
            return "heading:" + ((view as? NSTextField)?.stringValue ?? "")
        }) == signature { return }
        secondaryActions.arrangedSubviews.forEach { $0.removeFromSuperview() }
        var index = 0
        for group in groups {
            if let heading = group.heading {
                let label = NSTextField(labelWithString: heading)
                label.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
                label.textColor = .secondaryLabelColor
                secondaryActions.addArrangedSubview(label)
            }
            for action in group.actions {
                let button = NSButton(title: action.0, target: self, action: #selector(secondaryPressed(_:)))
                button.bezelStyle = .rounded
                button.lineBreakMode = .byTruncatingTail
                button.toolTip = action.0
                button.tag = index
                index += 1
                secondaryActions.addArrangedSubview(button)
                button.widthAnchor.constraint(lessThanOrEqualToConstant: 420).isActive = true
            }
        }
    }

    @objc private func secondaryPressed(_ sender: NSButton) {
        guard secondaryHandlers.indices.contains(sender.tag) else { return }
        secondaryHandlers[sender.tag]()
    }

    @objc private func pressed(_ sender: Any?) { onPress?() }

    /// Presses the button, as a click does.
    func press() { onPress?() }
}
