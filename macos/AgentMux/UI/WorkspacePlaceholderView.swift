import AppKit

/// The placeholder's button. As the default button it takes Return, but
/// never from a field being typed in, such as the Go to… list's above it.
@MainActor
final class WorkspacePlaceholderButton: NSButton {
    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if window?.firstResponder is NSText || window?.firstResponder is WorkspaceInspectorView { return false }
        return super.performKeyEquivalent(with: event)
    }
}

/// What the pane shows when it has no terminal to show: a title, a line of
/// explanation, and at most one thing to do about it.
@MainActor
final class WorkspacePlaceholderView: NSView {
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
    func show(title: String, message: String, button label: String? = nil, isDefault: Bool = false, secondary: [(String, () -> Void)] = [], onPress: (() -> Void)? = nil) {
        showSecondaryActions(secondary)
        titleLabel.stringValue = title
        messageLabel.stringValue = message
        button.title = label ?? ""
        button.isHidden = label == nil
        button.keyEquivalent = isDefault && label != nil ? "\r" : ""
        self.onPress = onPress
    }

    func showSecondaryActions(_ actions: [(String, () -> Void)]) {
        secondaryHandlers = actions.map { $0.1 }
        secondaryActions.isHidden = actions.isEmpty
        if secondaryActions.arrangedSubviews.compactMap({ ($0 as? NSButton)?.title }) == actions.map({ $0.0 }) { return }
        secondaryActions.arrangedSubviews.forEach { $0.removeFromSuperview() }
        for (index, action) in actions.enumerated() {
            let button = NSButton(title: action.0, target: self, action: #selector(secondaryPressed(_:)))
            button.bezelStyle = .rounded
            button.tag = index
            secondaryActions.addArrangedSubview(button)
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
