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
    var label: String {
        if failed { return "Refresh failed" }
        if warning { return "Read warning" }
        if !loading, let success { return success.message }
        return loading && showProgress ? "Refreshing…" : "Read status"
    }
    var body: some View {
        Button { showing = true } label: {
            HStack(spacing: 4) {
                Image(systemName: failed ? "exclamationmark.circle" : warning ? "exclamationmark.triangle" : success != nil ? "checkmark.circle" : "clock")
                Text(label).lineLimit(1)
            }.foregroundStyle(failed ? Color.red : warning ? .orange : success != nil ? .green : .secondary)
                .frame(width: 116, height: 24, alignment: .trailing)
        }.buttonStyle(.plain).font(.caption)
            .help(reads.map(\.description).joined(separator: "\n"))
            .accessibilityLabel("Read status. " + reads.map(\.description).joined(separator: ". "))
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

struct WorkspaceReadDetails: View {
    let reads: [WorkspaceReadObservation]
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                ForEach(Array(reads.enumerated()), id: \.offset) { _, read in
                    VStack(alignment: .leading, spacing: 5) {
                        Text(read.title).font(.headline)
                        if read.showingPrevious { Text("Previous results remain visible.").foregroundStyle(.orange) }
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
                Text("Read times describe the displayed content, not the agent’s last activity.").font(.caption).foregroundStyle(.secondary)
            }.textSelection(.enabled).padding(16)
        }.frame(width: 380, height: 300)
    }
}
