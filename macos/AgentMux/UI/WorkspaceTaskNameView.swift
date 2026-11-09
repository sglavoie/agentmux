import SwiftUI

struct WorkspaceTaskNameView: View {
    @ObservedObject var model: WorkspaceTaskName
    @FocusState private var editing: Bool
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            TextField("e.g. Fix login redirect", text: $model.name).focused($editing)
                .accessibilityLabel("Task name")
            if !model.suggestion.isEmpty {
                Button { model.name = model.suggestion; editing = true } label: {
                    VStack(alignment: .leading, spacing: 3) {
                        Text("Use suggested title").font(.caption.weight(.semibold))
                        Text(model.suggestion).font(.callout).lineLimit(2)
                    }.frame(maxWidth: .infinity, alignment: .leading)
                }.buttonStyle(.borderless)
            } else if model.loading { Text("Looking for a saved title…").font(.caption).foregroundStyle(.secondary) }
            Text("Names appear across the workspace and carry forward when you hand work to another agent. Clear the field to remove the name.")
                .font(.caption).foregroundStyle(.secondary)
        }.frame(width: 340, height: 145).onAppear { editing = true }
    }
}
