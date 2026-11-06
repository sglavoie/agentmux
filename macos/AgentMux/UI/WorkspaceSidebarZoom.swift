import AppKit
import SwiftUI

@MainActor
protocol WorkspaceSidebarZoomAware: AnyObject {
    func setSidebarZoom(_ zoom: CGFloat)
}

/// Scales the whole sidebar, including row spacing and controls. The split
/// view still measures its width in window points; content lays out in the
/// smaller or larger logical viewport and keeps native scrolling/hit testing.
@MainActor
final class WorkspaceSidebarZoomView: NSView {
    private let canvas = NSView()
    private let content: NSView
    var zoom: CGFloat = 1 {
        didSet { needsLayout = true }
    }
    /// Paints pure white over the source-list material in the light
    /// appearance, so the left sidebar matches the inspector; the dark
    /// appearance keeps the material.
    var opaqueInLight = false {
        didSet { updateFill() }
    }

    init(content: NSView) {
        self.content = content
        super.init(frame: NSRect(x: 0, y: 0, width: 300, height: 700))
        autoresizesSubviews = false
        canvas.autoresizesSubviews = false
        content.translatesAutoresizingMaskIntoConstraints = true
        content.autoresizingMask = []
        canvas.frame = bounds
        content.frame = canvas.bounds
        addSubview(canvas)
        canvas.addSubview(content)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        updateFill()
    }

    private func updateFill() {
        let dark = effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        wantsLayer = true
        layer?.backgroundColor = opaqueInLight && !dark ? NSColor.white.cgColor : nil
    }

    override func layout() {
        // A collapsed split item can briefly have no extent. Keep its last
        // viewport rather than asking its controls to fit into zero points.
        if bounds.width > 0, bounds.height > 0 {
            canvas.frame = bounds
            canvas.bounds = NSRect(origin: .zero, size: NSSize(width: bounds.width / zoom, height: bounds.height / zoom))
            content.frame = canvas.bounds
            (content as? WorkspaceSidebarZoomAware)?.setSidebarZoom(zoom)
        }
        super.layout()
    }
}

/// Claim Option zoom before the focused terminal can encode Option as text.
/// Physical keys also recognize layouts where Option produces ≠, ±, or º.
@MainActor
final class WorkspaceZoomWindow: NSWindow {
    enum Zoom { case bigger, smaller, reset }
    var onSidebarZoom: ((Zoom) -> Void)?

    override func sendEvent(_ event: NSEvent) {
        if performSidebarZoom(with: event) { return }
        super.sendEvent(event)
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        performSidebarZoom(with: event) || super.performKeyEquivalent(with: event)
    }

    private func performSidebarZoom(with event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection([.command, .control, .option, .shift])
        if event.type == .keyDown, let onSidebarZoom {
            switch (event.keyCode, flags) {
            case (24, .option), (24, [.option, .shift]), (69, .option):
                onSidebarZoom(.bigger)
                return true
            case (27, .option), (78, .option):
                onSidebarZoom(.smaller)
                return true
            case (29, .option), (82, .option):
                onSidebarZoom(.reset)
                return true
            default: break
            }
        }
        return false
    }
}

/// SwiftUI roots embedded in an AppKit-scaled sidebar.
protocol WorkspaceSidebarHostedContent: View {
    var width: CGFloat? { get set }
    var zoom: CGFloat { get set }
}

/// Cancel the ancestor's AppKit transform at the hosting boundary. SwiftUI
/// scales its own content so rendering and pointer coordinates stay together.
@MainActor
final class WorkspaceSidebarHostingView<Content: WorkspaceSidebarHostedContent>: NSHostingView<Content> {
    var zoom: CGFloat = 1 {
        didSet {
            guard zoom != oldValue else { return }
            rootView.zoom = zoom
            updateBounds()
            invalidateIntrinsicContentSize()
        }
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        if newSize.width > 0, rootView.width != newSize.width {
            rootView.width = newSize.width
        }
        updateBounds()
    }

    private func updateBounds() {
        let size = NSSize(width: frame.width * zoom, height: frame.height * zoom)
        if bounds.size != size { setBoundsSize(size) }
    }

    override var intrinsicContentSize: NSSize {
        let size = super.intrinsicContentSize
        return NSSize(width: size.width / zoom, height: size.height / zoom)
    }
}

struct WorkspaceSidebarScaleLayout: Layout {
    let zoom: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let size = subviews[0].sizeThatFits(.init(width: proposal.width.map { $0 / zoom },
                                                height: proposal.height.map { $0 / zoom }))
        return CGSize(width: size.width * zoom, height: size.height * zoom)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews[0].place(at: bounds.origin, anchor: .topLeading,
                          proposal: .init(width: bounds.width / zoom, height: bounds.height / zoom))
    }
}
