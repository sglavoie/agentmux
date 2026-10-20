import SwiftUI

struct WorkspaceChoice: Identifiable, Equatable {
    let id: String
    let label: String
    let detail: String
    var problem: String?
}

enum WorkspaceChoiceNavigation {
    static func highlight(in choices: [WorkspaceChoice], keeping id: String?) -> String? {
        let available = choices.filter { $0.problem == nil }
        return available.first(where: { $0.id == id })?.id ?? available.first?.id
    }

    static func move(_ delta: Int, in choices: [WorkspaceChoice], from id: String?) -> String? {
        let available = choices.filter { $0.problem == nil }
        guard !available.isEmpty else { return nil }
        let index = available.firstIndex { $0.id == id } ?? (delta > 0 ? -1 : available.count)
        return available[max(0, min(available.count - 1, index + delta))].id
    }
}

/// Search by identity, display name, or directory. The bound value is always a
/// catalog identity; typed search text can never become a launch argument.
struct WorkspaceChoicePicker: View {
    let title: String
    @Binding var selection: String
    let label: String
    let defaultLabel: String
    let choices: [WorkspaceChoice]
    @State private var presented = false
    @State private var query = ""
    @State private var highlighted: String?
    @FocusState private var searching: Bool
    @FocusState private var openerFocused: Bool

    private var filtered: [WorkspaceChoice] {
        let all = [WorkspaceChoice(id: "", label: defaultLabel, detail: "")] + choices
        return all.filter { query.isEmpty || ($0.label + " " + $0.id + " " + $0.detail).localizedCaseInsensitiveContains(query) }
    }

    var body: some View {
        LabeledContent(title) {
            Button {
                query = ""
                highlighted = WorkspaceChoiceNavigation.highlight(in: filtered, keeping: selection)
                presented = true
            } label: {
                HStack { Text(label); Spacer(); Image(systemName: "chevron.up.chevron.down") }
            }.accessibilityLabel("\(title): \(label)")
                .focused($openerFocused)
                .onChange(of: presented) { _, open in if !open { openerFocused = true } }
                .popover(isPresented: $presented) {
                    VStack(alignment: .leading, spacing: 8) {
                        TextField("Search \(title.lowercased()) choices", text: $query)
                            .textFieldStyle(.roundedBorder).focused($searching)
                            .onSubmit { chooseHighlighted() }
                            .onKeyPress(.downArrow) { move(1); return .handled }
                            .onKeyPress(.upArrow) { move(-1); return .handled }
                            .onChange(of: query) { _, _ in
                                highlighted = WorkspaceChoiceNavigation.highlight(in: filtered, keeping: highlighted)
                            }
                        if filtered.isEmpty { Text("No matching choices").foregroundStyle(.secondary) }
                        else if highlighted == nil { Text("No available matches. See the reasons below.").foregroundStyle(.secondary) }
                        ScrollViewReader { proxy in
                            ScrollView {
                                LazyVStack(alignment: .leading, spacing: 4) {
                                    ForEach(filtered) { choice in
                                        Button { choose(choice) } label: {
                                            VStack(alignment: .leading, spacing: 3) {
                                                Text(choice.label)
                                                if !choice.detail.isEmpty { Text(choice.detail).font(.caption).foregroundStyle(.secondary) }
                                                if let problem = choice.problem { Text(problem).font(.caption).foregroundStyle(WorkspaceReviewColors.warning) }
                                            }.frame(maxWidth: .infinity, alignment: .leading).padding(6)
                                                .background(highlighted == choice.id ? Color.accentColor.opacity(0.15) : .clear)
                                        }.buttonStyle(.plain).disabled(choice.problem != nil).id(choice.id)
                                            .accessibilityAddTraits(highlighted == choice.id ? .isSelected : [])
                                    }
                                }
                            }.onChange(of: highlighted) { _, id in if let id { proxy.scrollTo(id) } }
                                .onAppear { if let highlighted { proxy.scrollTo(highlighted) } }
                        }
                    }.padding(12).frame(width: 440, height: 300)
                        .onAppear { searching = true }
                        .onChange(of: choices) { _, _ in
                            highlighted = WorkspaceChoiceNavigation.highlight(in: filtered, keeping: highlighted)
                        }
                        .onExitCommand { presented = false }
                }
        }
    }

    private func choose(_ choice: WorkspaceChoice) {
        guard choice.problem == nil else { return }
        selection = choice.id; presented = false
    }
    private func chooseHighlighted() {
        if let choice = filtered.first(where: { $0.id == highlighted && $0.problem == nil }) { choose(choice) }
    }
    private func move(_ delta: Int) {
        highlighted = WorkspaceChoiceNavigation.move(delta, in: filtered, from: highlighted)
    }
}
