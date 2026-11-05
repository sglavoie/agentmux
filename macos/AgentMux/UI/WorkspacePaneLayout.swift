import AppKit

/// Two containers, each with a visible focus control. Moving containers never
/// creates a terminal or changes ownership of a running session.
@MainActor
final class WorkspacePaneLayout: NSSplitView, NSSplitViewDelegate {
    static let minimumPaneWidth: CGFloat = 470
    final class Host: NSView {
        let body: NSView
        let heading = WorkspacePaneHeadingButton(title: "", target: nil, action: nil)
        let sessionContext = WorkspaceSessionContextView()
        var onFocus: (() -> Void)?
        private var headerHeight: NSLayoutConstraint!
        private var splitHeading = false
        func showHeading(_ show: Bool) {
            splitHeading = show
            updateHeaderHeight()
        }

        func showContext(_ context: WorkspaceSessionContext?, canAnswer: Bool) {
            sessionContext.show(context, canAnswer: canAnswer)
            updateHeaderHeight()
        }

        private func updateHeaderHeight() {
            let hasContext = sessionContext.context != nil
            heading.isHidden = !splitHeading && !hasContext
            headerHeight.constant = hasContext ? 56 : splitHeading ? 28 : 0
        }

        init(body: NSView) {
            self.body = body
            super.init(frame: NSRect(x: 0, y: 0, width: 800, height: 700))
            heading.bezelStyle = .inline
            heading.alignment = .left
            heading.cell?.lineBreakMode = .byTruncatingMiddle
            heading.target = self
            heading.action = #selector(focusPane)
            heading.setAccessibilityLabel("Focus pane")
            body.frame = bounds
            body.translatesAutoresizingMaskIntoConstraints = false
            heading.translatesAutoresizingMaskIntoConstraints = false
            sessionContext.translatesAutoresizingMaskIntoConstraints = false
            addSubview(body)
            addSubview(heading)
            addSubview(sessionContext)
            headerHeight = body.topAnchor.constraint(equalTo: topAnchor)
            NSLayoutConstraint.activate([
                heading.topAnchor.constraint(equalTo: topAnchor),
                heading.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
                heading.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
                heading.heightAnchor.constraint(equalToConstant: 28),
                sessionContext.topAnchor.constraint(equalTo: heading.bottomAnchor),
                sessionContext.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
                sessionContext.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
                sessionContext.heightAnchor.constraint(equalToConstant: 24),
                headerHeight,
                body.leadingAnchor.constraint(equalTo: leadingAnchor),
                body.trailingAnchor.constraint(equalTo: trailingAnchor),
                body.bottomAnchor.constraint(equalTo: bottomAnchor),
            ])
        }

        required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
        @objc private func focusPane() { onFocus?() }


    }

    let active: Host
    let other: Host
    private var arrangement: Int?
    var onResize: (() -> Void)?
    var fraction: Double? {
        guard arrangedSubviews.count == 2, bounds.width > 0 else { return nil }
        return Double(arrangedSubviews[0].frame.width / bounds.width)
    }
    func restoreFraction(_ fraction: Double) {
        guard fraction.isFinite, arrangedSubviews.count == 2 else { return }
        let position = CGFloat(min(0.9, max(0.1, fraction))) * bounds.width
        setPosition(min(max(Self.minimumPaneWidth, position), max(Self.minimumPaneWidth, bounds.width - Self.minimumPaneWidth - dividerThickness)), ofDividerAt: 0)
    }
    func splitViewDidResizeSubviews(_ notification: Notification) { onResize?() }

    init(active: NSView, other: NSView) {
        self.active = Host(body: active)
        self.other = Host(body: other)
        super.init(frame: NSRect(x: 0, y: 0, width: 800, height: 700))
        isVertical = true
        dividerStyle = .thin
        delegate = self
        arrange(split: false, activeOnRight: false)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func arrange(split: Bool, activeOnRight: Bool) {
        let next = split ? (activeOnRight ? 2 : 1) : 0
        guard next != arrangement else { return }
        let divider = arrangement == 0 ? bounds.width / 2 : (arrangedSubviews.first?.frame.width ?? bounds.width / 2)
        arrangement = next
        active.removeFromSuperview()
        other.removeFromSuperview()
        active.showHeading(split)
        if split, activeOnRight { addArrangedSubview(other) }
        addArrangedSubview(active)
        if split, !activeOnRight { addArrangedSubview(other) }
        adjustSubviews()
        if split { setPosition(divider, ofDividerAt: 0) }
        active.needsLayout = true
        other.needsLayout = true
    }

    func splitView(_ splitView: NSSplitView, constrainMinCoordinate proposed: CGFloat, ofSubviewAt index: Int) -> CGFloat {
        min(Self.minimumPaneWidth, bounds.width / 2)
    }

    func splitView(_ splitView: NSSplitView, constrainMaxCoordinate proposed: CGFloat, ofSubviewAt index: Int) -> CGFloat {
        max(bounds.width / 2, bounds.width - Self.minimumPaneWidth - dividerThickness)
    }

    func titles(active title: String, other otherTitle: String) {
        active.heading.title = headingTitle(title, context: active.sessionContext.context, active: true)
        active.heading.toolTip = active.heading.title
        active.heading.contentTintColor = .controlAccentColor
        other.heading.title = headingTitle(otherTitle, context: other.sessionContext.context, active: false)
        other.heading.toolTip = other.heading.title
        other.heading.contentTintColor = .secondaryLabelColor
    }

    private func headingTitle(_ fallback: String, context: WorkspaceSessionContext?, active: Bool) -> String {
        let title = context.map { $0.title + ($0.location.isEmpty ? "" : " · " + $0.location) } ?? fallback
        return (active ? "●  " : "○  ") + title
    }
}

/// The pane title remains readable when the workspace is not the key window.
/// The whole title still acts as the native, keyboard-accessible focus button.
final class WorkspacePaneHeadingButton: NSButton {
    override func draw(_ dirtyRect: NSRect) {
        if isHighlighted {
            NSColor.controlAccentColor.withAlphaComponent(0.08).setFill()
            NSBezierPath(roundedRect: bounds, xRadius: 5, yRadius: 5).fill()
        }
        let paragraph = NSMutableParagraphStyle()
        paragraph.lineBreakMode = .byTruncatingMiddle
        let font = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
        let text = NSAttributedString(string: title, attributes: [
            .font: font, .foregroundColor: NSColor.labelColor, .paragraphStyle: paragraph,
        ])
        text.draw(in: NSRect(x: 4, y: (bounds.height - font.boundingRectForFont.height) / 2,
                            width: max(0, bounds.width - 8), height: font.boundingRectForFont.height))
    }
}
