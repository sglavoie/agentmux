import SwiftUI

/// Lives at the bottom of the right inspector, never over terminal output or input.
struct WorkspaceWelcomeView: WorkspaceSidebarHostedContent {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @ObservedObject var model: WorkspaceWelcomeModel
    var width: CGFloat?
    var zoom: CGFloat = 1

    var body: some View {
        if model.visible {
            WorkspaceSidebarScaleLayout(zoom: zoom) {
                card.frame(width: width)
                    .fixedSize(horizontal: false, vertical: true)
                    .scaleEffect(zoom, anchor: .topLeading)
            }
        }
    }

    private func milestoneRow(_ milestone: WorkspaceMilestoneWatch.Milestone) -> some View {
        HStack(spacing: 6) {
            WorkspaceMissionPatch(id: milestone.id, title: milestone.title, project: "", shippedAt: "", compact: true)
                .scaleEffect(0.5).frame(width: 35, height: 40)
            VStack(alignment: .leading, spacing: 2) {
                Text("Milestone unlocked").font(.caption).foregroundStyle(.secondary)
                Text(milestone.title).font(.caption.weight(.semibold))
                Button("Open Flight Log") { model.onAction(.flightLog) }
                    .buttonStyle(.link).font(.caption)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Milestone unlocked: \(milestone.title)")
        .accessibilityAction(named: "Open Flight Log") { model.onAction(.flightLog) }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(model.title).font(.system(size: 12, weight: .semibold)).lineLimit(1).help(model.title)
                Spacer(minLength: 4)
                Button { model.dismiss() } label: {
                    Image(systemName: "xmark").font(.system(size: 9, weight: .medium))
                        .frame(width: 24, height: 24).contentShape(Rectangle())
                }
                    .buttonStyle(.plain).help("Dismiss summary").accessibilityLabel("Dismiss summary")
            }
            Text(model.detail).font(.system(size: 11)).foregroundStyle(.secondary)
                .lineLimit(3).help(model.detail)
                .fixedSize(horizontal: false, vertical: true)
            if let milestone = model.receipt?.milestone {
                milestoneRow(milestone).transition(.opacity)
            }
            if model.receipt?.title != "All caught up" {
                Text(model.attentionText).font(.system(size: 11, weight: .medium))
                    .lineLimit(2).help(model.attentionText)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if model.receipt != nil && model.remainingWaiting > 0 && model.live {
                Button("Next waiting · \(model.remainingWaiting) remaining") { model.onAction(.nextWaiting) }
                    .controlSize(.small)
            }
            Button("Open Briefing") { model.onAction(.briefing) }
                .buttonStyle(.link).font(.system(size: 11)).fixedSize()
        }
        .animation(reduceMotion ? nil : .easeIn(duration: 0.2), value: model.receipt?.milestone)
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.035), in: RoundedRectangle(cornerRadius: 8))
        .padding(.horizontal, 8).padding(.bottom, 4)
        .accessibilityElement(children: .contain)
    }
}
