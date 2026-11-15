import SwiftUI

struct WorkspaceReturnOutcome: View {
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
