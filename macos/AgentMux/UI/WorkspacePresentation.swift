import SwiftUI

/// Prose is shared by History, Activity and Waiting; terminal/code sizing stays independent.
struct WorkspaceReadingMenu: View {
    @ObservedObject var preferences: WorkspaceReadingPreferences = .shared
    var body: some View {
        Menu {
            Text("Reading text: \(Int(preferences.effectiveProseSize)) pt")
            Button("Larger reading text") { preferences.proseSize = min(24, preferences.effectiveProseSize + 1) }.disabled(preferences.effectiveProseSize >= 24)
            Button("Smaller reading text") { preferences.proseSize = max(11, preferences.effectiveProseSize - 1) }.disabled(preferences.effectiveProseSize <= 11)
            Button("Reset reading text size") { preferences.proseSize = 13 }
            Divider()
            Text("Code and terminal output: \(Int(preferences.effectiveFontSize)) pt")
            Button("Larger monospace text") { preferences.fontSize = min(24, preferences.effectiveFontSize + 1) }.disabled(preferences.effectiveFontSize >= 24)
            Button("Smaller monospace text") { preferences.fontSize = max(10, preferences.effectiveFontSize - 1) }.disabled(preferences.effectiveFontSize <= 10)
            Button("Reset monospace text size") { preferences.fontSize = 12 }
        } label: { Image(systemName: "textformat.size") }
            .menuStyle(.borderlessButton).fixedSize().help("Reading text size").accessibilityLabel("Reading text size")
    }
}

/// A permanent toolbar slot: neither delayed progress nor a long failure changes
/// the reader's geometry. Full, selectable details remain available in a popover.
struct WorkspaceStatus: View {
    var loading = false
    var title = "Refreshing…"
    var retained = false
    var showTarget = false
    var feedback: WorkspaceFeedback?
    var recover: (() -> Void)?
    @State private var showProgress = false
    @State private var showDetails = false

    private var color: Color {
        switch feedback?.kind {
        case .error: return .red
        case .warning: return .orange
        case .success: return .green
        default: return .secondary
        }
    }
    private var symbol: String {
        switch feedback?.kind {
        case .error: return "exclamationmark.circle"
        case .warning: return "exclamationmark.triangle"
        case .success: return "checkmark.circle"
        default: return "info.circle"
        }
    }
    var body: some View {
        HStack(spacing: 6) {
            if retained && (loading || feedback?.kind == .error) {
                Text("Previous results").font(.callout).foregroundStyle(.secondary).fixedSize()
            }
            if let feedback {
                Button { showDetails = true } label: {
                    HStack(spacing: 4) {
                        Image(systemName: symbol)
                        if showTarget { Text(feedback.target).fontWeight(.semibold).lineLimit(1).truncationMode(.middle).frame(maxWidth: 140) }
                        Text(feedback.message).lineLimit(1).truncationMode(.tail)
                    }.foregroundStyle(color)
                }
                .buttonStyle(.plain)
                .help([feedback.target, feedback.message, feedback.nextStep].filter { !$0.isEmpty }.joined(separator: "\n"))
                .accessibilityLabel("\(feedback.target): \(feedback.message). Show details")
                .popover(isPresented: $showDetails) {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 10) {
                            Text(feedback.target).font(.headline)
                            Text(feedback.message)
                            if !feedback.nextStep.isEmpty { Text(feedback.nextStep).foregroundStyle(.secondary) }
                            if let recovery = feedback.recovery, let recover {
                                Button(recovery.title) { recover() }.disabled(loading)
                            }
                        }.textSelection(.enabled).padding(16).frame(maxWidth: .infinity, alignment: .leading)
                    }.frame(width: 380, height: 220)
                }
                if let recovery = feedback.recovery, let recover {
                    Button(recovery.title, action: recover).disabled(loading).fixedSize()
                }
            } else if loading && showProgress {
                ProgressView().controlSize(.small)
                Text(title).lineLimit(1).foregroundStyle(.secondary)
                    .help(retained ? "\(title) Showing the previous results." : title)
                    .accessibilityLabel(retained ? "\(title) Showing the previous results." : title)
            }
            Spacer(minLength: 0)
        }
        .font(.caption).frame(height: 24)
        .task(id: loading) {
            showProgress = false
            guard loading else { return }
            do { try await Task.sleep(for: .milliseconds(350)) } catch { return }
            guard !Task.isCancelled else { return }
            showProgress = true
        }
    }
}

/// Paths keep their identifying suffix, while copying and accessibility retain
/// the complete value. Use the same treatment in lists and review headings.
struct WorkspacePath: View {
    let path: String
    var display: String? = nil
    var body: some View {
        Text(display ?? path).lineLimit(1).truncationMode(.head).help(path)
            .textSelection(.enabled).accessibilityLabel(path)
    }
}

/// The same acknowledgement for code and diff copies; failures are visible too.
struct WorkspaceCopyFeedback: Equatable {
    var message: String?
    var succeeded = false
    mutating func copy(_ text: String, to pasteboard: NSPasteboard = .general) {
        pasteboard.clearContents()
        succeeded = pasteboard.setString(text, forType: .string)
        message = succeeded ? "Copied" : "Copy failed. Try again."
    }
}

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
        applying(to: AttributedString(text), terms: terms)
    }

    static func applying(to attributed: AttributedString, terms: [String]) -> AttributedString {
        var result = attributed
        let text = String(result.characters)
        for term in terms where !term.isEmpty {
            var start = text.startIndex
            while start < text.endIndex, let range = text.range(of: term, options: .caseInsensitive, range: start..<text.endIndex) {
                if let lower = AttributedString.Index(range.lowerBound, within: result),
                   let upper = AttributedString.Index(range.upperBound, within: result) {
                    result[lower..<upper].backgroundColor = .yellow.opacity(0.35)
                }
                start = range.upperBound
            }
        }
        return result
    }
}

/// Refresh relative ages without fetching data. The tooltip retains the exact
/// source timestamp, including its timezone, for comparison and diagnostics.
struct WorkspaceTimestamp: View {
    let title: String
    let value: String
    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            Text("\(title): \(WorkspaceWording.relativeTime(value, now: context.date))")
                .font(.caption).foregroundStyle(.secondary).help(value)
                .accessibilityLabel("\(title): \(value)")
        }
    }
}
