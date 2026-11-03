import SwiftUI

struct WorkspaceRecapView: View {
    @ObservedObject var model: WorkspaceRecapModel
    @State private var expandedClaims: Set<String> = []
    var generationWaitMessage = "Will update after the agent finishes its turn."
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("Conversation Recap").font(.headline)
                Spacer()
                if model.loading { ProgressView().controlSize(.small) }
                Button { model.refresh() } label: {
                    if model.automaticallyGenerate { Image(systemName: "arrow.clockwise") }
                    else { Text(model.report?.recap == nil ? "Generate Recap" : "Regenerate") }
                }
                    .buttonStyle(.borderless).disabled(model.loading || !model.canGenerate || model.report?.status == "disabled")
                    .accessibilityLabel(model.report?.recap == nil ? "Generate conversation recap" : "Regenerate conversation recap")
                    .help("Generate a new recap using the configured summarization profile")
            }
            if let recap = model.report?.recap {
                claim("Goal", recap.goal)
                claim("Latest", recap.latest)
                claim("Outstanding", recap.outstanding)
                let date = WorkspaceActivityEntry.date(recap.generatedAt)
                Text("Updated \(date?.formatted(date: .abbreviated, time: .shortened) ?? recap.generatedAt) · \(recap.profile) · \(recap.model)")
                    .font(.caption).foregroundStyle(.secondary)
                if model.report?.status != "ready" || model.error != nil {
                    Text("Saved recap · newer messages may not be included").font(.caption).foregroundStyle(.secondary)
                }
                if recap.partial {
                    Text("Based on excerpts from the conversation").font(.caption).foregroundStyle(.secondary)
                }
            } else if !model.loading {
                Text(emptyMessage).font(.caption).foregroundStyle(.secondary)
            }
            if !model.canGenerate {
                Text(generationWaitMessage).font(.caption).foregroundStyle(.secondary)
            }
            if let warning = model.error ?? model.report?.warning {
                Text(warning).font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .textSelection(.enabled)
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 8))
        .accessibilityIdentifier("conversation-recap")
        .onChange(of: model.conversation) { _, _ in expandedClaims.removeAll() }
    }

    private var emptyMessage: String {
        switch model.report?.status {
        case "disabled": return "Automatic recaps are disabled in [defaults.recaps]."
        case "unconfigured": return "Choose a Claude profile in [defaults.recaps] to generate recaps."
        case "empty": return "No conversation messages to recap yet."
        case "pending": return "Recap generation is in progress."
        default: return model.automaticallyGenerate ? "A recap will appear here when it is ready." : "No current saved recap. Generate one to see the goal, latest outcome, and unfinished work."
        }
    }

    @ViewBuilder private func claim(_ title: String, _ claim: ConversationRecap.Claim) -> some View {
        if !claim.text.reveal().isEmpty {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                Text(claim.text.reveal()).font(.callout).lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true).help(claim.text.reveal())
                let messages = Array(Set(claim.messages)).sorted()
                if !messages.isEmpty {
                    VStack(alignment: .leading, spacing: 4) {
                        Button {
                            if !expandedClaims.insert(title).inserted { expandedClaims.remove(title) }
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: expandedClaims.contains(title) ? "chevron.down" : "chevron.right")
                                Text("Supporting messages")
                            }
                            .contentShape(Rectangle())
                        }
                        .accessibilityLabel("\(title) supporting messages")
                        .accessibilityValue(expandedClaims.contains(title) ? "Expanded" : "Collapsed")
                        .accessibilityIdentifier("recap-supporting-\(title.lowercased())")
                        if expandedClaims.contains(title) {
                            let canOpenMessages = model.report?.status == "ready" && model.error == nil && !model.loading
                            ForEach(messages, id: \.self) { index in
                                Button("Message \(index + 1)") { model.reveal(index) }
                                    .accessibilityIdentifier("recap-evidence-\(title.lowercased())-\(index)")
                                    .disabled(!canOpenMessages)
                            }
                            .padding(.leading, 14)
                            if !canOpenMessages {
                                Text(model.loading ? "Checking supporting messages…" : "Refresh recap to open messages.")
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                    .buttonStyle(.borderless)
                    .font(.caption)
                    .textSelection(.disabled)
                }
            }
        }
    }
}
