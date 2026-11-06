import SwiftUI

/// Lives below the session list, never over terminal output or input.
struct WorkspaceWelcomeView: View {
    @ObservedObject var model: WorkspaceWelcomeModel

    var body: some View {
        if model.visible { card }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(model.title).font(.system(size: 12, weight: .semibold)).lineLimit(1).help(model.title)
                Spacer(minLength: 4)
                Button { model.dismiss() } label: { Image(systemName: "xmark").font(.system(size: 9, weight: .medium)) }
                    .buttonStyle(.plain).help("Dismiss summary").accessibilityLabel("Dismiss summary")
            }
            Text(model.detail).font(.system(size: 11)).foregroundStyle(.secondary)
                .lineLimit(3).help(model.detail)
                .fixedSize(horizontal: false, vertical: true)
            if model.receipt?.title != "All caught up" {
                Text(model.attentionText).font(.system(size: 11, weight: .medium))
                    .lineLimit(2).help(model.attentionText)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if model.waiting > 0 && model.live {
                Button("Next waiting") { model.onAction(.nextWaiting) }
                    .controlSize(.small)
            }
            HStack(spacing: 10) {
                Button("Activity") { model.onAction(.activity) }
                Button("Briefing") { model.onAction(.briefing) }
            }.buttonStyle(.link).font(.system(size: 11))
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 8))
        .padding(.horizontal, 8).padding(.bottom, 4)
        .accessibilityElement(children: .contain)
    }
}
