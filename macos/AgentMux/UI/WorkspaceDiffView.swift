import AppKit
import SwiftUI

/// TextKit keeps large patches selectable and scrollable without constructing
/// thousands of SwiftUI views. Positions belong to a checkout/base/file tuple.
struct WorkspaceDiffView: NSViewRepresentable {
    let diff: WorkspaceDiff
    let identity: String
    let loading: Bool
    let jump: Int?
    let jumpSequence: Int

    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = true
        scroll.borderType = .noBorder
        let text = NSTextView(frame: scroll.contentView.bounds)
        text.isEditable = false
        text.isSelectable = true
        text.isRichText = true
        text.isHorizontallyResizable = true
        text.isVerticallyResizable = true
        text.autoresizingMask = [.width]
        text.minSize = NSSize(width: 0, height: 0)
        text.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        text.textContainer?.containerSize = text.maxSize
        text.textContainer?.widthTracksTextView = false
        text.textContainerInset = NSSize(width: 8, height: 10)
        text.setAccessibilityLabel("Unified diff, old and new line numbers")
        scroll.documentView = text
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let text = scroll.documentView as? NSTextView else { return }
        let state = context.coordinator
        // Keep the old text and position until the replacement has arrived.
        guard !loading else { return }
        if state.identity != identity || state.diff != diff {
            if let previous = state.identity { state.positions[previous] = scroll.contentView.bounds.origin }
            let position = state.positions[identity] ?? .zero
            state.identity = identity
            state.diff = diff
            let rendered = Self.render(diff)
            state.hunkRanges = rendered.hunks
            text.textStorage?.setAttributedString(rendered.text)
            text.layoutManager?.ensureLayout(for: text.textContainer!)
            scroll.contentView.scroll(to: position)
            scroll.reflectScrolledClipView(scroll.contentView)
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

    static func render(_ diff: WorkspaceDiff) -> (text: NSAttributedString, hunks: [NSRange]) {
        let result = NSMutableAttributedString()
        var hunks: [NSRange] = []
        let digits = max(3, String(diff.lines.compactMap { max($0.old ?? 0, $0.new ?? 0) }.max() ?? 0).count)
        func number(_ n: Int?) -> String { let s = n.map(String.init) ?? ""; return String(repeating: " ", count: max(0, digits - s.count)) + s }
        for line in diff.lines {
            let prefix = "\(number(line.old)) \(number(line.new)) │ "
            var attributes: [NSAttributedString.Key: Any] = [
                .font: NSFont.monospacedSystemFont(ofSize: 12, weight: .regular),
                .foregroundColor: NSColor.labelColor
            ]
            switch line.kind {
            case .added: attributes[.backgroundColor] = NSColor.systemGreen.withAlphaComponent(0.14)
            case .removed: attributes[.backgroundColor] = NSColor.systemRed.withAlphaComponent(0.14)
            case .hunk:
                attributes[.backgroundColor] = NSColor.controlAccentColor.withAlphaComponent(0.14)
                attributes[.font] = NSFont.monospacedSystemFont(ofSize: 12, weight: .semibold)
                hunks.append(NSRange(location: result.length, length: (prefix + line.text).utf16.count))
            case .metadata: attributes[.foregroundColor] = NSColor.secondaryLabelColor
            case .context: break
            }
            let row = NSMutableAttributedString(string: prefix + line.text + "\n", attributes: attributes)
            row.addAttribute(.foregroundColor, value: NSColor.secondaryLabelColor, range: NSRange(location: 0, length: prefix.utf16.count))
            result.append(row)
        }
        return (result, hunks)
    }

    final class Coordinator {
        var identity: String?
        var diff: WorkspaceDiff?
        var positions: [String: NSPoint] = [:]
        var hunkRanges: [NSRange] = []
        var jumpSequence = 0
    }
}
