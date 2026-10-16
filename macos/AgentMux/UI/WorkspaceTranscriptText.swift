import AppKit
import SwiftUI

/// A bounded transcript page already owns message identity and scrolling. Blocks
/// only format its text; they never replace those message anchors or load URLs.
enum WorkspaceTranscriptBlock: Equatable {
    case paragraph(String)
    case heading(Int, String)
    case item(String, String)
    case code(language: String, text: String)

    static func parse(_ text: String) -> [Self] {
        let lines = text.components(separatedBy: "\n")
        var blocks: [Self] = []
        var prose: [String] = []
        var index = 0
        func flush() {
            if !prose.isEmpty { blocks.append(.paragraph(prose.joined(separator: "\n"))); prose = [] }
        }
        while index < lines.count {
            let line = lines[index]
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if let marker = trimmed.first, marker == "`" || marker == "~" {
                let fence = trimmed.prefix(while: { $0 == marker })
                if fence.count >= 3 {
                    flush()
                    let language = String(trimmed.dropFirst(fence.count)).trimmingCharacters(in: .whitespaces)
                    var code: [String] = []
                    index += 1
                    while index < lines.count {
                        let closing = lines[index].trimmingCharacters(in: .whitespaces)
                        if closing.count >= fence.count && closing.allSatisfy({ $0 == marker }) { break }
                        code.append(lines[index]); index += 1
                    }
                    blocks.append(.code(language: language, text: code.joined(separator: "\n")))
                    index += 1
                    continue
                }
            }
            let hashes = trimmed.prefix(while: { $0 == "#" }).count
            if (1...6).contains(hashes), trimmed.dropFirst(hashes).first == " " {
                flush(); blocks.append(.heading(hashes, String(trimmed.dropFirst(hashes + 1))))
            } else if trimmed.hasPrefix("- ") || trimmed.hasPrefix("* ") || trimmed.hasPrefix("+ ") {
                flush(); blocks.append(.item("•", String(trimmed.dropFirst(2))))
            } else if let dot = trimmed.firstIndex(of: "."),
                      !trimmed[..<dot].isEmpty, trimmed[..<dot].allSatisfy({ $0.isNumber }),
                      trimmed[trimmed.index(after: dot)...].hasPrefix(" ") {
                flush(); blocks.append(.item(String(trimmed[...dot]), String(trimmed[trimmed.index(after: dot)...].dropFirst())))
            } else if trimmed.isEmpty {
                flush()
            } else { prose.append(line) }
            index += 1
        }
        flush()
        return blocks
    }

    static func inline(_ text: String, terms: [String]) -> AttributedString {
        let parsed = (try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(text)
        return WorkspaceHighlight.applying(to: parsed, terms: terms)
    }
}

struct WorkspaceTranscriptText: View {
    private let blocks: [WorkspaceTranscriptBlock]
    let terms: [String]

    init(text: String, terms: [String]) {
        blocks = WorkspaceTranscriptBlock.parse(text)
        self.terms = terms
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(blocks.indices, id: \.self) { index in
                switch blocks[index] {
                case let .paragraph(text):
                    Text(WorkspaceTranscriptBlock.inline(text, terms: terms)).lineSpacing(3)
                case let .heading(level, text):
                    Text(WorkspaceTranscriptBlock.inline(text, terms: terms))
                        .font(level <= 2 ? .title3.weight(.semibold) : .headline)
                        .accessibilityAddTraits(.isHeader).padding(.top, 4)
                case let .item(marker, text):
                    HStack(alignment: .top, spacing: 8) {
                        Text(marker).foregroundStyle(.secondary)
                        Text(WorkspaceTranscriptBlock.inline(text, terms: terms)).lineSpacing(3)
                    }
                case let .code(language, text):
                    WorkspaceTranscriptCode(language: language, text: text, terms: terms)
                }
            }
        }.textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct WorkspaceTranscriptCode: View {
    let language: String
    let text: String
    let terms: [String]
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(language.isEmpty ? "Code" : language).font(.caption.weight(.medium))
                Spacer()
                Button {
                    NSPasteboard.general.clearContents()
                    copied = NSPasteboard.general.setString(text, forType: .string)
                } label: { Label(copied ? "Copied" : "Copy code", systemImage: copied ? "checkmark" : "doc.on.doc") }
                    .buttonStyle(.borderless).help("Copy this code block without Markdown fences")
            }.padding(8)
            Divider()
            ScrollView(.horizontal) {
                Text(WorkspaceHighlight.applying(to: AttributedString(text), terms: terms))
                    .font(.system(.body, design: .monospaced)).fixedSize(horizontal: true, vertical: false).padding(10)
            }
        }.background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.primary.opacity(0.15)))
            .onChange(of: text) { _, _ in copied = false }
    }
}
