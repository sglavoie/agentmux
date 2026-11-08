import AppKit
import SwiftUI
import Combine

/// One thing known about the shown session: what it is, and its value. An
/// identifier is copied by a click on its line.
@MainActor
final class WorkspaceInspectorFieldView: NSView {
    static let labelWidth: CGFloat = 84
    static let copied = "Copied"

    private(set) var field: WorkspaceInspection.Field
    var onCopy: ((String) -> Void)?
    let nameLabel = NSTextField(labelWithString: "")
    let valueLabel = NSTextField(labelWithString: "")
    /// Counts copies, so only the last one's moment ends the "Copied".
    private var copies = 0
    /// Whether the value says "Copied" for now.
    var showsCopied: Bool { copies > 0 }

    init(_ field: WorkspaceInspection.Field) {
        self.field = field
        super.init(frame: .zero)
        nameLabel.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        nameLabel.textColor = .secondaryLabelColor
        valueLabel.font = field.copies
            ? .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
            : .systemFont(ofSize: NSFont.smallSystemFontSize)
        valueLabel.lineBreakMode = .byTruncatingMiddle
        if ["Reason", "Evidence", "Observed"].contains(field.label) {
            valueLabel.lineBreakMode = .byWordWrapping
            valueLabel.usesSingleLineMode = false
            valueLabel.cell?.wraps = true
            valueLabel.maximumNumberOfLines = 0
        }
        valueLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for label in [nameLabel, valueLabel] {
            label.translatesAutoresizingMaskIntoConstraints = false
            // The line is said as one; its labels are not said again.
            label.setAccessibilityElement(false)
            addSubview(label)
        }
        NSLayoutConstraint.activate([
            nameLabel.leadingAnchor.constraint(equalTo: leadingAnchor),
            nameLabel.widthAnchor.constraint(equalToConstant: Self.labelWidth),
            nameLabel.firstBaselineAnchor.constraint(equalTo: valueLabel.firstBaselineAnchor),
            valueLabel.leadingAnchor.constraint(equalTo: nameLabel.trailingAnchor, constant: 6),
            valueLabel.trailingAnchor.constraint(equalTo: trailingAnchor),
            valueLabel.topAnchor.constraint(equalTo: topAnchor),
            valueLabel.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        update(field)
    }

    /// Shows `field` on this line: the same field with its value now. A
    /// value that says "Copied" goes on saying so for its moment.
    func update(_ field: WorkspaceInspection.Field) {
        self.field = field
        nameLabel.stringValue = field.label
        if !showsCopied { valueLabel.stringValue = field.value }
        toolTip = field.copies ? "\(field.value) — click to copy" : field.value
        // A view is not an accessibility element until it says so; an
        // identifier's line is a button that copies it.
        setAccessibilityElement(true)
        setAccessibilityRole(field.copies ? .button : .staticText)
        setAccessibilityLabel("\(field.label): \(field.value)")
        setAccessibilityHelp(field.copies ? "Copies the \(field.label.lowercased())" : nil)
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) is not supported for this view")
    }

    override func hitTest(_ point: NSPoint) -> NSView? {
        super.hitTest(point) == nil ? nil : self
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func mouseDown(with event: NSEvent) {}

    override func mouseUp(with event: NSEvent) {
        guard bounds.contains(convert(event.locationInWindow, from: nil)) else { return }
        copyValue()
    }

    /// Copies the value and says so for a moment; nothing for a line that is
    /// not an identifier.
    func copyValue() {
        guard field.copies else { return }
        onCopy?(field.value)
        valueLabel.stringValue = Self.copied
        WorkspaceCopyNotice.announce(Self.copied, from: self)
        copies += 1
        DispatchQueue.main.asyncAfter(deadline: .now() + WorkspaceCopyNotice.seconds) { [weak self] in
            MainActor.assumeIsolated {
                guard let self else { return }
                self.copies -= 1
                if !self.showsCopied { self.valueLabel.stringValue = self.field.value }
            }
        }
    }

    override func accessibilityPerformPress() -> Bool {
        guard field.copies else { return false }
        copyValue()
        return true
    }
}
