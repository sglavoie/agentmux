import SwiftUI

/// Shared presentation for the native read/review windows. A failed read and an
/// empty successful read have distinct treatments; retained content is labeled.
struct WorkspaceNotice: View {
    let message: String
    var kind: Kind = .information
    var actionTitle = "Try Again"
    var action: (() -> Void)?
    enum Kind { case information, warning, error }
    private var color: Color { kind == .error ? .red : kind == .warning ? .orange : .secondary }
    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: kind == .error ? "exclamationmark.circle" : kind == .warning ? "exclamationmark.triangle" : "info.circle")
                .foregroundStyle(color)
            Text(message).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
            if let action { Button(actionTitle, action: action).fixedSize() }
        }
        .font(.callout).padding(10)
        .background(color.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
    }
}

struct WorkspaceLoading: View {
    let title: String
    var retained = false
    var body: some View {
        HStack(spacing: 8) {
            ProgressView().controlSize(.small)
            Text(retained ? "\(title) Showing the previous results." : title)
                .font(.callout).foregroundStyle(.secondary)
        }.accessibilityElement(children: .combine)
    }
}

struct WorkspaceEmpty: View {
    let title: String
    let detail: String
    var symbol = "tray"
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: symbol).font(.title2).foregroundStyle(.secondary)
            Text(title).font(.headline)
            Text(detail).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }.padding(20).frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

enum WorkspaceHighlight {
    static func text(_ text: String, terms: [String]) -> AttributedString {
        var result = AttributedString(text)
        for term in terms where !term.isEmpty {
            var start = text.startIndex
            while start < text.endIndex, let range = text.range(of: term, options: .caseInsensitive, range: start..<text.endIndex) {
                if let lower = AttributedString.Index(range.lowerBound, within: result),
                   let upper = AttributedString.Index(range.upperBound, within: result) {
                    result[lower..<upper].backgroundColor = .yellow.opacity(0.35)
                    result[lower..<upper].font = .body.bold()
                }
                start = range.upperBound
            }
        }
        return result
    }
}
