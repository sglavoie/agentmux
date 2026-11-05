import AppKit
import SwiftUI

/// Fixed-height chrome: changes to status and saved outcomes never resize
/// the terminal. A popover reads saved recaps only when explicitly opened.
@MainActor
final class WorkspaceSessionContextView: NSView, NSPopoverDelegate {
    let status = NSTextField(labelWithString: "")
    let question = NSButton(title: "View question", target: nil, action: nil)
    let outcome = NSButton(title: "Last outcome", target: nil, action: nil)
    let recap: WorkspaceRecapModel
    private(set) var context: WorkspaceSessionContext?
    private let popover = NSPopover()
    var onQuestion: (() -> Void)?

    init(recap: WorkspaceRecapModel? = nil) {
        self.recap = recap ?? WorkspaceRecapModel(command: { nil }, automaticallyGenerate: false)
        super.init(frame: .zero)
        isHidden = true
        status.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        status.textColor = .secondaryLabelColor
        status.lineBreakMode = .byTruncatingTail
        status.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        for button in [question, outcome] {
            button.bezelStyle = .rounded
            button.controlSize = .small
            button.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            button.target = self
            button.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        question.action = #selector(showQuestion)
        outcome.action = #selector(showOutcome)
        outcome.toolTip = "Read the saved conversation outcome"
        let stack = NSStackView(views: [status, outcome, question])
        stack.orientation = .horizontal
        stack.spacing = 10
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        popover.behavior = .transient
        popover.delegate = self
        popover.contentViewController = NSHostingController(rootView: WorkspaceSavedOutcomeView(model: self.recap))
        popover.contentSize = NSSize(width: 380, height: 240)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    func show(_ context: WorkspaceSessionContext?, canAnswer: Bool) {
        if context?.key != self.context?.key || context?.conversation != self.context?.conversation {
            popover.close()
            recap.close()
        }
        self.context = context
        isHidden = context == nil
        status.stringValue = context?.status ?? ""
        status.toolTip = context?.status
        question.isHidden = !canAnswer
        outcome.isHidden = context?.conversation == nil
        if popover.isShown, let context {
            recap.select(context.conversation, revision: context.revision, canGenerate: false)
        }
    }

    @objc private func showQuestion() { onQuestion?() }

    @objc func showOutcome() {
        guard let context, context.conversation != nil else { return }
        recap.select(context.conversation, revision: context.revision, canGenerate: false)
        popover.show(relativeTo: outcome.bounds, of: outcome, preferredEdge: .minY)
    }

    func popoverDidClose(_ notification: Notification) { recap.close() }
}

private struct WorkspaceSavedOutcomeView: View {
    @ObservedObject var model: WorkspaceRecapModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Text("Last outcome").font(.headline)
                    Spacer()
                    Button("Refresh") { model.check() }.disabled(model.loading)
                }
                if let recap = model.report?.recap {
                    Text(recap.latest.text.reveal()).textSelection(.enabled)
                    Text("Saved \(WorkspaceActivityEntry.date(recap.generatedAt)?.formatted(date: .abbreviated, time: .shortened) ?? recap.generatedAt)")
                        .font(.caption).foregroundStyle(.secondary)
                    if recap.partial { Text("Based on conversation excerpts").font(.caption) }
                    if model.report?.status != "ready" || model.error != nil {
                        Text("Newer messages may not be included.").font(.caption)
                    }
                    ForEach(Array(Set(recap.latest.messages)).sorted(), id: \.self) { index in
                        Button("Read supporting message \(index + 1)") { model.reveal(index) }
                            .disabled(model.loading || model.error != nil || model.report?.status != "ready")
                    }
                } else {
                    Text(model.loading ? "Reading saved outcome…" : "No saved outcome yet. Open the Session inspector for a conversation recap.")
                        .foregroundStyle(.secondary)
                }
                if let warning = model.error ?? model.report?.warning {
                    Text(warning).font(.caption).foregroundStyle(.secondary)
                }
            }.padding(16).frame(maxWidth: .infinity, alignment: .leading)
        }.frame(width: 380, height: 240)
    }
}
