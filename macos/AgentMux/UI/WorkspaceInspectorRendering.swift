import AppKit
import SwiftUI
import Combine

extension WorkspaceInspectorView {
    /// Everything the sidebar shows.
    struct Model: Equatable {
        var tab = WorkspaceInspectorTab.waiting
        var waiting: [WorkspaceWaitingRow] = []
        var waitingGroup: String?
        var showsAllWaitingGroups = false
        /// Nil when the pane is on no session.
        var inspection: WorkspaceInspection?
        var sourceConversation: ConversationIdentity?
        /// False while the rows are held from the last live snapshot.
        var current = true
        /// What is wrong with the feed, said instead of "nothing waiting":
        /// a daemon that is down cannot say that nothing is.
        var feedNotice: String?

        /// The lines under the tabs, top to bottom. The session tab with no
        /// session says what is wrong with the feed when something is: the
        /// pane may still be on a session nobody can list. Held lines say it
        /// above them, so what they show is not taken for what is so now.
        var lines: [WorkspaceInspectorLine] {
            let held: [WorkspaceInspectorLine] = !current ? feedNotice.map { [.note($0)] } ?? [] : []
            switch tab {
            case .waiting:
                if waiting.isEmpty { return [.note(feedNotice ?? WorkspaceInspectorView.nothingWaiting)] }
                return held + waiting.map(WorkspaceInspectorLine.waiting)
            case .session:
                guard let inspection else { return [.note(feedNotice ?? WorkspaceInspectorView.noSession)] }
                let title = [inspection.glyph, inspection.title].filter { !$0.isEmpty }.joined(separator: " ")
                return held + [.title(title, toolTip: inspection.title)]
                    + inspection.sections.flatMap { section in
                        [.heading(section.title)] + section.fields.map(WorkspaceInspectorLine.field)
                    }
                    + [.heading("Actions"), .actions(muted: inspection.muted, embedded: inspection.embedded)]
            }
        }
    }

    /// Whether a line is drawn dimmed: every held line but a note.
    static func dims(_ line: WorkspaceInspectorLine, current: Bool) -> Bool {
        guard !current else { return false }
        if case .note = line { return false }
        return true
    }

    nonisolated static let nothingWaiting = "Nothing is waiting on you."
    nonisolated static let noSession = "No session is shown."
    nonisolated static let unavailableForSession = "This session cannot use this action."
    nonisolated static let waitForDaemon = "Available once the daemon answers again."

    func view(for line: WorkspaceInspectorLine) -> NSView {
        switch line {
        case let .note(text):
            return Self.note(text)
        case let .title(text, toolTip):
            let title = NSTextField(labelWithString: text)
            title.font = .systemFont(ofSize: NSFont.systemFontSize, weight: .semibold)
            title.lineBreakMode = .byTruncatingTail
            title.toolTip = toolTip
            return title
        case let .heading(text):
            return Self.heading(text)
        case let .field(field):
            let view = WorkspaceInspectorFieldView(field)
            view.onCopy = { [weak self] value in self?.onCopy(value) }
            return view
        case let .waiting(row):
            let view = WorkspaceWaitingRowView(row)
            view.onPress = { [weak self, weak view] in
                if let key = view?.row.key { self?.onWaiting?(key) }
            }
            view.onAnswer = { [weak self, weak view] answer in
                guard let self, let view, self.rowIsCurrent(view.row), !self.answerWasSent(view.row) else { return }
                self.lastWaiting = view.row.key
                self.onAnswer?(answer)
            }
            view.onRefreshContext = { [weak self, weak view] in
                if let row = view?.row { self?.refreshContext(row) }
            }
            view.onContext = { [weak self, weak view] in
                if let row = view?.row { self?.toggleContext(row) }
            }
            return view
        case let .actions(muted, embedded):
            return actions(muted: muted, embedded: embedded)
        }
    }

    /// Shows `line` on the view that showed the same line before.
    func update(_ view: NSView, to line: WorkspaceInspectorLine) {
        switch line {
        case let .note(text):
            (view as? NSTextField)?.stringValue = text
        case let .title(text, toolTip):
            (view as? NSTextField)?.stringValue = text
            view.toolTip = toolTip
        case .heading:
            break
        case let .field(field):
            (view as? WorkspaceInspectorFieldView)?.update(field)
        case let .waiting(row):
            (view as? WorkspaceWaitingRowView)?.update(row)
        case let .actions(muted, embedded):
            for (button, action) in zip(actionButtons, WorkspaceInspectorAction.allCases) {
                Self.configure(button, action, muted: muted, embedded: embedded)
            }
        }
    }

    private static func note(_ text: String) -> NSTextField {
        let label = NSTextField(wrappingLabelWithString: text)
        label.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
        label.textColor = .secondaryLabelColor
        return label
    }

    private static func heading(_ text: String) -> NSView {
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
        label.textColor = .secondaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        // The room above a heading sets its section apart.
        let holder = NSView()
        holder.addSubview(label)
        NSLayoutConstraint.activate([
            label.topAnchor.constraint(equalTo: holder.topAnchor, constant: 8),
            label.leadingAnchor.constraint(equalTo: holder.leadingAnchor),
            label.trailingAnchor.constraint(lessThanOrEqualTo: holder.trailingAnchor),
            label.bottomAnchor.constraint(equalTo: holder.bottomAnchor),
        ])
        return holder
    }

    /// The buttons, two to a line. One a session cannot use is
    /// disabled and says so.
    private func actions(muted: Bool, embedded: Bool) -> NSView {
        let buttons = WorkspaceInspectorAction.allCases.enumerated().map { index, action in
            let button = NSButton(title: "", target: self, action: #selector(actionPressed(_:)))
            button.tag = index
            button.bezelStyle = .rounded
            button.controlSize = .small
            button.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            button.refusesFirstResponder = true
            Self.configure(button, action, muted: muted, embedded: embedded)
            return button
        }
        let rows = stride(from: 0, to: buttons.count, by: 2).map { Array(buttons[$0..<min($0 + 2, buttons.count)]) }
        let grid = NSGridView(views: rows)
        grid.rowSpacing = 4
        grid.columnSpacing = 6
        for column in 0..<grid.numberOfColumns { grid.column(at: column).xPlacement = .fill }
        if buttons.count > 1 { buttons[0].widthAnchor.constraint(equalTo: buttons[1].widthAnchor).isActive = true }
        return grid
    }

    private static func configure(_ button: NSButton, _ action: WorkspaceInspectorAction, muted: Bool, embedded: Bool) {
        button.title = action.title(muted: muted)
        button.isEnabled = embedded || !action.needsEmbedded
        button.toolTip = button.isEnabled ? action.shortcut : Self.unavailableForSession
    }
}

/// Give SwiftUI the sidebar's actual width when it computes the card's
/// intrinsic height. An unconstrained ideal width undercounts wrapped lines.
struct WorkspaceInspectorRecapView: WorkspaceSidebarHostedContent {
    let model: WorkspaceRecapModel
    var width: CGFloat?
    var zoom: CGFloat = 1

    var body: some View {
        WorkspaceSidebarScaleLayout(zoom: zoom) {
            WorkspaceRecapView(model: model)
                .frame(width: width)
                .fixedSize(horizontal: false, vertical: true)
                .scaleEffect(zoom, anchor: .topLeading)
        }
    }
}

/// The inspector's lines, laid out from the top down so a short list sits
/// at the top of the sidebar.
@MainActor
final class WorkspaceInspectorStack: NSStackView {
    override var isFlipped: Bool { true }
}
