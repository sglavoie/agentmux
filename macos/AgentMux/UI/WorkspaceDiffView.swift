import AppKit
import SwiftUI

/// TextKit keeps large patches selectable; the ruler is decoration, never clipboard text.
struct WorkspaceDiffView: NSViewRepresentable {
    let diff: WorkspaceDiff
    let identity: String
    let loading: Bool
    let jump: Int?
    let jumpSequence: Int
    var reviewState: WorkspaceReviewState? = nil
    var fontSize: CGFloat = 12
    var wrapLines = false
    var onCopy: ((WorkspaceCopyFeedback) -> Void)?

    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeNSView(context: Context) -> WorkspaceDiffContainer {
        let container = WorkspaceDiffContainer()
        let scroll = container.scroll
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = true
        scroll.borderType = .noBorder
        scroll.automaticallyAdjustsContentInsets = false
        scroll.contentInsets = NSEdgeInsetsZero
        let text = WorkspaceDiffTextView(frame: scroll.contentView.bounds)
        text.isEditable = false
        text.isSelectable = true
        text.isRichText = true
        text.isVerticallyResizable = true
        text.autoresizingMask = [.width]
        text.minSize = .zero
        text.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        text.textContainerInset = NSSize(width: 8, height: 10)
        text.setAccessibilityLabel("Unified diff; copy selects source text without line numbers or diff markers")
        scroll.documentView = text
        context.coordinator.gutter = container.gutter
        context.coordinator.observe(scroll, store: reviewState)
        return container
    }

    func updateNSView(_ container: WorkspaceDiffContainer, context: Context) {
        let scroll = container.scroll
        guard let text = scroll.documentView as? WorkspaceDiffTextView else { return }
        text.onCopy = onCopy
        let state = context.coordinator
        guard !loading else { return }
        if state.identity != identity || state.diff != diff || state.fontSize != fontSize || state.wrapLines != wrapLines {
            if let previous = state.identity {
                state.positions[previous] = scroll.contentView.bounds.origin
                state.anchors[previous] = text.readingAnchor()
            }
            let position = state.positions[identity] ?? reviewState?.position(identity) ?? .zero
            let anchor = state.anchors[identity]
            state.identity = identity
            state.diff = diff
            state.fontSize = fontSize
            state.wrapLines = wrapLines
            let rendered = Self.render(diff, fontSize: fontSize)
            state.hunkRanges = rendered.hunks
            text.diff = diff
            text.lineRanges = rendered.lines
            text.textStorage?.setAttributedString(rendered.text)
            let digits = max(3, String(diff.lines.compactMap { max($0.old ?? 0, $0.new ?? 0) }.max() ?? 0).count)
            container.gutter.digits = digits
            container.gutterWidth = CGFloat(digits * 2 + 3) * fontSize * 0.61
            container.needsLayout = true
            container.layoutSubtreeIfNeeded()
            scroll.hasHorizontalScroller = !wrapLines
            text.isHorizontallyResizable = !wrapLines
            text.textContainer?.widthTracksTextView = wrapLines
            text.textContainer?.containerSize = NSSize(width: wrapLines ? scroll.contentSize.width : CGFloat.greatestFiniteMagnitude,
                                                       height: CGFloat.greatestFiniteMagnitude)
            if wrapLines { text.setFrameSize(NSSize(width: scroll.contentSize.width, height: text.frame.height)) }
            scroll.tile()
            text.layoutManager?.ensureLayout(for: text.textContainer!)
            var restored = position
            if let anchor, let y = text.position(for: anchor) { restored.y = y }
            if wrapLines { restored.x = 0 }
            scroll.contentView.scroll(to: restored)
            scroll.reflectScrolledClipView(scroll.contentView)
            state.gutter?.needsDisplay = true
        }
        if state.jumpSequence != jumpSequence {
            state.jumpSequence = jumpSequence
            if let jump, state.hunkRanges.indices.contains(jump) {
                let range = state.hunkRanges[jump]
                text.scrollRangeToVisible(range)
                text.showFindIndicator(for: range)
            }
        }
    }

    static func render(_ diff: WorkspaceDiff, fontSize: CGFloat = 12) -> (text: NSAttributedString, hunks: [NSRange], lines: [NSRange]) {
        let result = NSMutableAttributedString()
        var hunks: [NSRange] = [], ranges: [NSRange] = []
        for (index, line) in diff.lines.enumerated() {
            var attributes: [NSAttributedString.Key: Any] = [
                .font: NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular),
                .foregroundColor: NSColor.labelColor
            ]
            switch line.kind {
            case .added: attributes[.backgroundColor] = NSColor.systemGreen.withAlphaComponent(0.14)
            case .removed: attributes[.backgroundColor] = NSColor.systemRed.withAlphaComponent(0.14)
            case .hunk:
                attributes[.backgroundColor] = NSColor.controlAccentColor.withAlphaComponent(0.14)
                attributes[.font] = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .semibold)
                hunks.append(NSRange(location: result.length, length: line.text.utf16.count))
            case .metadata: attributes[.foregroundColor] = NSColor.secondaryLabelColor
            case .context: break
            }
            let row = line.text + (index + 1 < diff.lines.count ? "\n" : "")
            ranges.append(NSRange(location: result.length, length: row.utf16.count))
            result.append(NSAttributedString(string: row, attributes: attributes))
        }
        return (result, hunks, ranges)
    }

    final class Coordinator {
        weak var gutter: WorkspaceDiffGutter?
        private var observer: NSObjectProtocol?
        private var store: WorkspaceReviewState?
        func observe(_ scroll: NSScrollView, store: WorkspaceReviewState?) {
            self.store = store
            scroll.contentView.postsBoundsChangedNotifications = true
            observer = NotificationCenter.default.addObserver(forName: NSView.boundsDidChangeNotification,
                object: scroll.contentView, queue: .main) { [weak self, weak scroll] _ in
                guard let self, let scroll, let id = self.identity else { return }
                let point = scroll.contentView.bounds.origin
                self.positions[id] = point
                self.store?.savePosition(point, id: id)
                self.gutter?.needsDisplay = true
            }
        }
        deinit { if let observer { NotificationCenter.default.removeObserver(observer) } }
        var identity: String?
        var diff: WorkspaceDiff?
        var positions: [String: NSPoint] = [:]
        // Content anchors are memory-only; preferences never persist source text.
        var anchors: [String: WorkspaceDiffTextView.Anchor] = [:]
        var hunkRanges: [NSRange] = []
        var jumpSequence = 0
        var fontSize: CGFloat = 0
        var wrapLines = false
    }
}

final class WorkspaceDiffTextView: NSTextView {
    var diff = WorkspaceDiff("")
    var lineRanges: [NSRange] = []
    var onCopy: ((WorkspaceCopyFeedback) -> Void)?

    struct Anchor {
        let line: WorkspaceDiff.Line
        let index: Int
        let offset: CGFloat
    }

    func lineIndex(at character: Int) -> Int? {
        guard !lineRanges.isEmpty else { return nil }
        var low = 0, high = lineRanges.count
        while low < high {
            let middle = (low + high) / 2
            if lineRanges[middle].location <= character { low = middle + 1 } else { high = middle }
        }
        return max(0, low - 1)
    }

    func lineRect(_ index: Int) -> NSRect? {
        guard lineRanges.indices.contains(index), let layout = layoutManager, let container = textContainer else { return nil }
        let range = lineRanges[index]
        guard range.length > 0 else { return nil }
        let glyphs = layout.glyphRange(forCharacterRange: NSRange(location: range.location, length: 1), actualCharacterRange: nil)
        var rect = layout.boundingRect(forGlyphRange: glyphs, in: container)
        rect.origin.y += textContainerOrigin.y
        return rect
    }

    func readingAnchor() -> Anchor? {
        guard let scroll = enclosingScrollView, let layout = layoutManager, let container = textContainer else { return nil }
        let y = scroll.documentVisibleRect.minY
        let point = NSPoint(x: 0, y: max(0, y - textContainerOrigin.y))
        let character = layout.characterIndex(for: point, in: container, fractionOfDistanceBetweenInsertionPoints: nil)
        guard let index = lineIndex(at: character), diff.lines.indices.contains(index), let rect = lineRect(index) else { return nil }
        return Anchor(line: diff.lines[index], index: index, offset: y - rect.minY)
    }

    func position(for anchor: Anchor) -> CGFloat? {
        // Prefer the same content nearest its previous position, then a nearby source line.
        let matches = diff.lines.indices.filter { diff.lines[$0].text == anchor.line.text && diff.lines[$0].kind == anchor.line.kind }
        let index = matches.min { abs($0 - anchor.index) < abs($1 - anchor.index) }
            ?? diff.lines.indices.filter { diff.lines[$0].new != nil || diff.lines[$0].old != nil }.min {
                abs((diff.lines[$0].new ?? diff.lines[$0].old ?? 0) - (anchor.line.new ?? anchor.line.old ?? 0))
                < abs((diff.lines[$1].new ?? diff.lines[$1].old ?? 0) - (anchor.line.new ?? anchor.line.old ?? 0))
            }
        guard let index, let rect = lineRect(index) else { return nil }
        return max(0, rect.minY + anchor.offset)
    }

    /// Keeps partial selections and Unicode intact, removing only actual diff markers.
    func sourceText(in selection: NSRange) -> String {
        let raw = string as NSString
        var result = ""
        for (index, range) in lineRanges.enumerated() {
            guard diff.lines.indices.contains(index), [.added, .removed, .context].contains(diff.lines[index].kind), range.length > 0 else { continue }
            let content = NSRange(location: range.location + 1, length: range.length - 1)
            let intersection = NSIntersectionRange(selection, content)
            if intersection.length > 0 { result += raw.substring(with: intersection) }
        }
        return result
    }

    override func copy(_ sender: Any?) { copySource(to: .general) }

    func copySource(to pasteboard: NSPasteboard) {
        let value = sourceText(in: selectedRange())
        guard !value.isEmpty else { return }
        var feedback = WorkspaceCopyFeedback()
        feedback.copy(value, to: pasteboard)
        onCopy?(feedback)
    }

    @objc func copyUnifiedDiff(_ sender: Any?) {
        let range = selectedRange()
        let value = range.length > 0 ? (string as NSString).substring(with: range) : diff.patch
        var feedback = WorkspaceCopyFeedback()
        feedback.copy(value)
        onCopy?(feedback)
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        let menu = super.menu(for: event) ?? NSMenu()
        let item = NSMenuItem(title: "Copy Unified Diff", action: #selector(copyUnifiedDiff(_:)), keyEquivalent: "")
        item.target = self
        menu.addItem(item)
        return menu
    }
}

final class WorkspaceDiffContainer: NSView {
    let scroll = NSScrollView()
    let gutter = WorkspaceDiffGutter()
    var gutterWidth: CGFloat = 66
    override var isFlipped: Bool { true }
    override init(frame: NSRect) {
        super.init(frame: frame)
        gutter.scrollView = scroll
        addSubview(gutter)
        addSubview(scroll)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
    override func layout() {
        super.layout()
        gutter.frame = NSRect(x: 0, y: 0, width: gutterWidth, height: bounds.height)
        scroll.frame = NSRect(x: gutterWidth, y: 0, width: max(0, bounds.width - gutterWidth), height: bounds.height)
        gutter.needsDisplay = true
    }
}

final class WorkspaceDiffGutter: NSView {
    var digits = 3
    weak var scrollView: NSScrollView?
    override var isFlipped: Bool { true }
    override func draw(_ rect: NSRect) {
        guard let scroll = scrollView, let text = scroll.documentView as? WorkspaceDiffTextView,
              let layout = text.layoutManager, let container = text.textContainer else { return }
        NSColor.windowBackgroundColor.setFill()
        bounds.fill()
        let visible = scroll.documentVisibleRect
        let character = layout.characterIndex(for: NSPoint(x: 0, y: max(0, visible.minY - text.textContainerOrigin.y)),
                                                in: container, fractionOfDistanceBetweenInsertionPoints: nil)
        guard let first = text.lineIndex(at: character) else { return }
        let font = text.textStorage.flatMap { $0.length > 0 ? $0.attribute(.font, at: 0, effectiveRange: nil) as? NSFont : nil }
            ?? NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)
        let attributes: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: NSColor.secondaryLabelColor]
        func number(_ n: Int?) -> String { let s = n.map(String.init) ?? ""; return String(repeating: " ", count: max(0, digits - s.count)) + s }
        for index in first..<text.diff.lines.count {
            guard let lineRect = text.lineRect(index) else { continue }
            if lineRect.minY > visible.maxY { break }
            let line = text.diff.lines[index]
            let label = "\(number(line.old)) \(number(line.new))"
            (label as NSString).draw(at: NSPoint(x: 5, y: lineRect.minY - visible.minY), withAttributes: attributes)
        }
    }
}
