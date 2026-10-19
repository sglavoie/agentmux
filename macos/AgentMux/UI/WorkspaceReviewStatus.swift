import SwiftUI

/// One toolbar location, with independent observation times and recovery for
/// every read. The fixed slot never changes the reader's geometry.
struct WorkspaceReadObservation {
    let title: String
    var loading = false
    var retained = false
    var refreshedAt: Date?
    var feedback: WorkspaceFeedback?
    var retry: (() -> Void)?

    var showingPrevious: Bool { retained && (loading || feedback?.kind == .error) }
    var description: String {
        if let feedback { return title + ": " + feedback.message + (showingPrevious ? " · Previous results" : "") }
        if loading { return title + (retained ? ": Refreshing · Previous results" : ": Loading") }
        if let refreshedAt { return title + ": Last read " + refreshedAt.formatted(.dateTime.year().month().day().hour().minute().second().timeZone()) }
        return title + ": Not read yet"
    }
}

struct WorkspaceReviewStatus: View {
    let reads: [WorkspaceReadObservation]
    @State private var showing = false
    @State private var showProgress = false
    private var loading: Bool { reads.contains { $0.loading } }
    private var failed: Bool { reads.contains { $0.feedback?.kind == .error } }
    private var success: WorkspaceFeedback? { reads.compactMap(\.feedback).first { $0.kind == .success } }
    private var warning: Bool { reads.contains { $0.feedback?.kind == .warning } }
    var label: String { label(at: .now) }

    func label(at now: Date) -> String {
        if failed { return "Refresh failed" }
        if warning { return "Read warning" }
        if !loading, let success { return success.message }
        if loading && showProgress { return "Refreshing…" }
        // The oldest displayed observation sets the age: a new list read must
        // never make an older transcript or diff appear freshly updated.
        guard let at = reads.compactMap(\.refreshedAt).min() else { return "Not updated yet" }
        let seconds = max(0, Int(now.timeIntervalSince(at)))
        let age = seconds < 60 ? "\(seconds)s" : seconds < 3600 ? "\(seconds / 60)m" : seconds < 86400 ? "\(seconds / 3600)h" : "\(seconds / 86400)d"
        return "Updated \(age) ago"
    }
    var body: some View {
        TimelineView(.periodic(from: .now, by: 5)) { context in
            Button { showing = true } label: {
                HStack(spacing: 4) {
                    Image(systemName: failed ? "exclamationmark.circle" : warning ? "exclamationmark.triangle" : success != nil ? "checkmark.circle" : "clock")
                    Text(label(at: context.date)).lineLimit(1)
                }.foregroundStyle(failed ? Color.red : warning ? WorkspaceReviewColors.warning : success != nil ? .green : .secondary)
                    .frame(width: 116, height: 24, alignment: .trailing)
            }.buttonStyle(.plain).font(.caption)
                .help(reads.map(\.description).joined(separator: "\n"))
                .accessibilityLabel(label(at: context.date) + ". Show update details. " + reads.map(\.description).joined(separator: ". "))
                .popover(isPresented: $showing) {
                    WorkspaceReadDetails(reads: reads)
                }
                .task(id: loading) {
                    showProgress = false
                    guard loading else { return }
                    do { try await Task.sleep(for: .milliseconds(350)) } catch { return }
                    showProgress = true
                }
        }
    }
}

struct WorkspaceReadDetails: View {
    let reads: [WorkspaceReadObservation]
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                ForEach(Array(reads.enumerated()), id: \.offset) { _, read in
                    VStack(alignment: .leading, spacing: 5) {
                        Text(read.title).font(.headline)
                        if read.showingPrevious { Text("Previous results remain visible.").foregroundStyle(WorkspaceReviewColors.warning) }
                        if read.loading { Text("Refreshing…").foregroundStyle(.secondary) }
                        if let at = read.refreshedAt {
                            Text("Last read: " + at.formatted(.dateTime.year().month().day().hour().minute().second().timeZone()))
                        } else { Text("No successful read yet.").foregroundStyle(.secondary) }
                        if let feedback = read.feedback {
                            Text(feedback.message)
                            if !feedback.nextStep.isEmpty { Text(feedback.nextStep).foregroundStyle(.secondary) }
                        }
                        if let retry = read.retry { Button("Refresh " + read.title.lowercased(), action: retry).disabled(read.loading) }
                    }.frame(maxWidth: .infinity, alignment: .leading)
                }
                Text("The toolbar shows the oldest update below. These times describe the displayed content, not the agent’s last activity.").font(.caption).foregroundStyle(.secondary)
            }.textSelection(.enabled).padding(16)
        }.frame(width: 380, height: 300)
    }
}

/// Recovery stays beside the content it reloads. Full diagnostics and read times
/// remain available in the toolbar; long errors cannot push the reader offscreen.
struct WorkspaceReadFailure: View {
    let title: String
    let error: String?
    var retained = false
    var loading = false
    let retry: () -> Void

    var body: some View {
        if let error {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "exclamationmark.circle").foregroundStyle(.red)
                VStack(alignment: .leading, spacing: 3) {
                    Text(title + (retained ? " refresh failed" : " could not be loaded")).fontWeight(.semibold)
                    Text(error).lineLimit(2).help(error).textSelection(.enabled)
                    if retained { Text("Showing previously loaded \(title.lowercased()).").foregroundStyle(.secondary) }
                }.frame(maxWidth: .infinity, alignment: .leading)
                Button(loading ? "Retrying…" : "Retry", action: retry)
                    .disabled(loading).accessibilityLabel("Retry \(title.lowercased())")
            }.font(.callout).padding(8)
                .background(Color.red.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("read-failure-" + title)
        }
    }
}
