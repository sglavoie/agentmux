import SwiftUI

struct WorkspaceReturnSummaryView: View {
    @ObservedObject var model: WorkspaceReturnSummary
    let close: () -> Void
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(model.since == nil ? "Recent activity" : "Since you stepped away").font(.title2.weight(.semibold))
                        if let since = model.since { Text("Since " + since.formatted(date: .abbreviated, time: .shortened)).font(.caption).foregroundStyle(.secondary) }
                    }
                    Spacer()
                    Button(action: close) { Image(systemName: "xmark") }.buttonStyle(.plain).accessibilityLabel("Close return summary")
                }
                if !model.current { Label("Live status unavailable · showing saved context", systemImage: "wifi.slash").font(.caption) }
                if model.loading { ProgressView("Reading recent activity…").controlSize(.small) }
                ForEach(model.updates) { update in
                    VStack(alignment: .leading, spacing: 8) {
                        Text(update.title).font(.headline)
                        Text((model.current || update.row == nil ? "" : "Last known · ") + update.detail).font(.callout).foregroundStyle(.secondary)
                        if let recap = model.recaps[update.id] { WorkspaceReturnOutcome(model: recap) }
                        if let at = update.event?.date { Text("Recorded " + at.formatted(date: .abbreviated, time: .shortened)).font(.caption2).foregroundStyle(.secondary) }
                        HStack {
                            if let row = update.row {
                                if row.attention {
                                    Button("View question") { model.onAnswer?(row.key) }.disabled(!model.current)
                                } else if row.iconState == "stop" {
                                    Button("Review changes") { model.onReview?(row.key) }.disabled(!model.current)
                                }
                                Button("Open session") { model.onOpen?(update) }.disabled(!model.current)
                            } else { Button("Open Activity") { model.onActivity?() } }
                        }.controlSize(.small)
                    }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 10))
                }
                if !model.loading && model.updates.isEmpty && model.problem == nil { Text("No recorded changes while you were away.").foregroundStyle(.secondary) }
                if let problem = model.problem { Text(problem).font(.caption).foregroundStyle(.secondary) }
                HStack {
                    Button("Activity") { model.onActivity?() }
                    Spacer()
                    Button("Refresh") { model.refresh() }.disabled(model.loading)
                }
                if let at = model.refreshedAt { Text("Read " + at.formatted(date: .omitted, time: .shortened)).font(.caption2).foregroundStyle(.secondary) }
            }.padding(18)
        }.frame(width: 440, height: 480)
    }
}

private struct WorkspaceReturnOutcome: View {
    @ObservedObject var model: WorkspaceRecapModel
    var body: some View {
        if let recap = model.report?.recap {
            Text(recap.latest.text.reveal()).font(.callout).lineLimit(3).textSelection(.enabled)
            Text("Saved " + (WorkspaceActivityEntry.date(recap.generatedAt)?.formatted(date: .abbreviated, time: .shortened) ?? recap.generatedAt)
                 + (model.report?.status != "ready" || recap.partial || model.error != nil ? " · may omit newer messages" : ""))
                .font(.caption2).foregroundStyle(.secondary)
        } else if model.loading { Text("Reading saved outcome…").font(.caption).foregroundStyle(.secondary) }
    }
}
