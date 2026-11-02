import SwiftUI

struct ConversationLinksView: View {
    let links: [ConversationLink]
    let conversation: ConversationIdentity?
    var targetKey = ""
    var warning: String?
    let onOpen: (ConversationIdentity) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            ForEach(links.filter { $0.target == conversation && conversation != nil || !targetKey.isEmpty && $0.targetKey == targetKey }) { link in
                Button { onOpen(link.source) } label: {
                    Text("Continued from \(link.source.encoded)").multilineTextAlignment(.leading)
                }.help("Read the source conversation").accessibilityLabel("Continued from \(link.source.encoded)")
            }
            ForEach(links.filter { $0.source == conversation }) { link in
                if let target = link.target {
                    Button { onOpen(target) } label: {
                        Text("Continued in \(target.encoded)").multilineTextAlignment(.leading)
                    }.help("Read the receiving conversation").accessibilityLabel("Continued in \(target.encoded)")
                } else {
                    Text("Continuation launched · conversation not reported yet")
                        .foregroundStyle(.secondary)
                }
            }
            if let warning { Text("Conversation links unavailable: " + warning).foregroundStyle(.secondary).textSelection(.enabled) }
        }.font(.caption).buttonStyle(.link).frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityIdentifier("conversation-links")
    }
}

struct WorkspaceThreadsView: View {
    @ObservedObject var model: WorkspaceThreadsModel
    let onOpen: (ConversationIdentity) -> Void
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            ConversationLinksView(links: model.links, conversation: model.conversation, targetKey: model.targetKey,
                                  warning: model.error, onOpen: onOpen)
            if model.loading { ProgressView().controlSize(.small).accessibilityLabel("Reading conversation links") }
            Button("Refresh Conversation Links") { model.refresh() }.font(.caption).disabled(model.loading)
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
}
