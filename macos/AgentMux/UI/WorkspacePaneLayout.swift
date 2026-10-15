import AppKit

/// Two containers, each with a visible focus control. Moving containers never
/// creates a terminal or changes ownership of a running session.
@MainActor
final class WorkspacePaneLayout: NSSplitView, NSSplitViewDelegate {
    static let minimumPaneWidth: CGFloat = 470
    final class Host: NSView {
        let body: NSView
        let heading = NSButton(title: "", target: nil, action: nil)
        var onFocus: (() -> Void)?
        private var headerHeight: NSLayoutConstraint!
        func showHeading(_ show: Bool) {
            heading.isHidden = !show
            headerHeight.constant = show ? 28 : 0
        }

        init(body: NSView) {
            self.body = body
            super.init(frame: NSRect(x: 0, y: 0, width: 800, height: 700))
            heading.bezelStyle = .inline
            heading.alignment = .left
            heading.cell?.lineBreakMode = .byTruncatingTail
            heading.target = self
            heading.action = #selector(focusPane)
            heading.setAccessibilityLabel("Focus pane")
            body.frame = bounds
            body.translatesAutoresizingMaskIntoConstraints = false
            heading.translatesAutoresizingMaskIntoConstraints = false
            addSubview(body)
            addSubview(heading)
            headerHeight = heading.heightAnchor.constraint(equalToConstant: 28)
            NSLayoutConstraint.activate([
                heading.topAnchor.constraint(equalTo: topAnchor),
                heading.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
                heading.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
                headerHeight,
                body.topAnchor.constraint(equalTo: heading.bottomAnchor),
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
        active.heading.title = "●  \(title)"
        active.heading.contentTintColor = .controlAccentColor
        other.heading.title = "○  \(otherTitle) — click to focus"
        other.heading.contentTintColor = .secondaryLabelColor
    }
}
