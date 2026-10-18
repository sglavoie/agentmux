import AppKit
import SwiftUI

/// A bounded transcript page already owns message identity and scrolling. Blocks
/// only format its text; they never replace those message anchors or load URLs.
enum WorkspaceTranscriptBlock: Equatable {
    case paragraph(String)
    case heading(Int, String)
    case item(String, String)
    case nestedItem(marker: String, text: String, indent: Int)
    indirect case quote([WorkspaceTranscriptBlock])
    case table(headers: [String], rows: [[String]])
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
            if trimmed.hasPrefix(">") {
                flush()
                var quoted: [String] = []
                while index < lines.count {
                    let line = lines[index].trimmingCharacters(in: .whitespaces)
                    guard line.hasPrefix(">") else { break }
                    var content = String(line.dropFirst())
                    if content.hasPrefix(" ") { content.removeFirst() }
                    quoted.append(content)
                    index += 1
                }
                blocks.append(.quote(parse(quoted.joined(separator: "\n"))))
                continue
            }
            if index + 1 < lines.count, let headers = tableCells(line),
               let separators = tableCells(lines[index + 1]), headers.count == separators.count,
               separators.allSatisfy({ cell in
                   let rule = cell.trimmingCharacters(in: CharacterSet(charactersIn: ":"))
                   return rule.count >= 3 && rule.allSatisfy { $0 == "-" }
               }) {
                flush()
                index += 2
                var rows: [[String]] = []
                while index < lines.count, let cells = tableCells(lines[index]), cells.count == headers.count {
                    rows.append(cells); index += 1
                }
                blocks.append(.table(headers: headers, rows: rows))
                continue
            }
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
                flush()
                blocks.append(listItem(marker: "•", text: String(trimmed.dropFirst(2)), line: line))
            } else if let dot = trimmed.firstIndex(of: "."),
                      !trimmed[..<dot].isEmpty, trimmed[..<dot].allSatisfy({ $0.isNumber }),
                      trimmed[trimmed.index(after: dot)...].hasPrefix(" ") {
                flush()
                blocks.append(listItem(marker: String(trimmed[...dot]), text: String(trimmed[trimmed.index(after: dot)...].dropFirst()), line: line))
            } else if trimmed.isEmpty {
                flush()
            } else { prose.append(line) }
            index += 1
        }
        flush()
        return blocks
    }

    private static func listItem(marker: String, text: String, line: String) -> Self {
        let indent = line.prefix(while: { $0 == " " || $0 == "\t" }).reduce(0) { $0 + ($1 == "\t" ? 4 : 1) }
        return indent == 0 ? .item(marker, text) : .nestedItem(marker: marker, text: text, indent: indent)
    }

    /// Escaped pipes and pipes inside inline code belong to their cell.
    static func tableCells(_ line: String) -> [String]? {
        var cells: [String] = [], cell = "", escaped = false, code = false, separator = false
        for character in line.trimmingCharacters(in: .whitespaces) {
            if escaped {
                if character != "|" { cell.append("\\") }
                cell.append(character); escaped = false
            } else if character == "\\" { escaped = true }
            else if character == "`" { code.toggle(); cell.append(character) }
            else if character == "|" && !code {
                cells.append(cell.trimmingCharacters(in: .whitespaces)); cell = ""; separator = true
            } else { cell.append(character) }
        }
        if escaped { cell.append("\\") }
        cells.append(cell.trimmingCharacters(in: .whitespaces))
        guard separator else { return nil }
        if cells.first == "" { cells.removeFirst() }
        if cells.last == "" { cells.removeLast() }
        return cells.isEmpty ? nil : cells
    }

    static func inline(_ text: String, terms: [String]) -> AttributedString {
        let parsed = (try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(text)
        return WorkspaceHighlight.applying(to: parsed, terms: terms)
    }
}

struct WorkspaceTranscriptText: View {
    private let blocks: [WorkspaceTranscriptBlock]
    let terms: [String]
    let preferences: WorkspaceReadingPreferences

    @MainActor init(text: String, terms: [String], preferences: WorkspaceReadingPreferences? = nil) {
        blocks = WorkspaceTranscriptBlock.parse(text)
        self.terms = terms
        self.preferences = preferences ?? .shared
    }

    var body: some View { WorkspaceTranscriptBlocks(blocks: blocks, terms: terms, preferences: preferences) }
}

private struct WorkspaceTranscriptBlocks: View {
    let blocks: [WorkspaceTranscriptBlock]
    let terms: [String]
    @ObservedObject var preferences: WorkspaceReadingPreferences
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(blocks.indices, id: \.self) { index in
                switch blocks[index] {
                case let .paragraph(text):
                    Text(WorkspaceTranscriptBlock.inline(text, terms: terms)).lineSpacing(3)
                case let .heading(level, text):
                    Text(WorkspaceTranscriptBlock.inline(text, terms: terms))
                        .font(.system(size: preferences.effectiveProseSize + (level <= 2 ? 3 : 0), weight: .semibold))
                        .accessibilityAddTraits(.isHeader).padding(.top, 4)
                case let .item(marker, text):
                    HStack(alignment: .top, spacing: 8) {
                        Text(marker).foregroundStyle(.secondary)
                        Text(WorkspaceTranscriptBlock.inline(text, terms: terms)).lineSpacing(3)
                    }
                case let .nestedItem(marker, text, indent):
                    HStack(alignment: .top, spacing: 8) {
                        Text(marker).foregroundStyle(.secondary)
                        Text(WorkspaceTranscriptBlock.inline(text, terms: terms)).lineSpacing(3)
                    }.padding(.leading, CGFloat(min(indent, 24)) * 6)
                case let .quote(blocks):
                    HStack(alignment: .top, spacing: 10) {
                        Rectangle().fill(Color.secondary.opacity(0.4)).frame(width: 3)
                        AnyView(WorkspaceTranscriptBlocks(blocks: blocks, terms: terms, preferences: preferences))
                    }.fixedSize(horizontal: false, vertical: true).padding(.vertical, 4)
                case let .table(headers, rows):
                    ScrollView(.horizontal) {
                        Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 8) {
                            GridRow {
                                ForEach(headers.indices, id: \.self) { column in
                                    Text(WorkspaceTranscriptBlock.inline(headers[column], terms: terms)).fontWeight(.semibold)
                                }
                            }
                            Divider().gridCellUnsizedAxes(.horizontal)
                            ForEach(rows.indices, id: \.self) { row in
                                GridRow {
                                    ForEach(headers.indices, id: \.self) { column in
                                        Text(WorkspaceTranscriptBlock.inline(rows[row][column], terms: terms))
                                            .frame(maxWidth: 280, alignment: .leading)
                                    }
                                }
                            }
                        }.padding(8)
                    }.background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
                case let .code(language, text):
                    WorkspaceTranscriptCode(language: language, text: text, terms: terms, preferences: preferences)
                }
            }
        }.font(.system(size: preferences.effectiveProseSize))
            .textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct WorkspaceTranscriptCode: View {
    let language: String
    let text: String
    let terms: [String]
    @ObservedObject var preferences: WorkspaceReadingPreferences
    var pasteboard: NSPasteboard = .general
    @State private var feedback = WorkspaceCopyFeedback()

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(language.isEmpty ? "Code" : language).font(.caption.weight(.medium))
                Spacer()
                Button {
                    feedback.copy(text, to: pasteboard)
                } label: { Label(feedback.message ?? "Copy code", systemImage: feedback.succeeded ? "checkmark" : "doc.on.doc") }
                    .buttonStyle(.borderless).help("Copy this code block without Markdown fences")
                    .font(.callout).foregroundStyle(.primary)
                    .accessibilityLabel(feedback.message ?? "Copy code")
                    .accessibilityIdentifier("transcript-copy-code")
            }.padding(8)
            Divider()
            if preferences.wrapLines {
                codeText.fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(10)
            } else {
                ScrollView(.horizontal) {
                    codeText.fixedSize(horizontal: true, vertical: false).padding(10)
                }
            }
        }.background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.primary.opacity(0.15)))
            .accessibilityElement(children: .contain)
            .accessibilityLabel(language.isEmpty ? "Code block" : "\(language) code block")
            .onChange(of: text) { _, _ in feedback = WorkspaceCopyFeedback() }
            .task(id: feedback.revision) {
                guard feedback.succeeded else { return }
                do { try await Task.sleep(for: .seconds(2)) } catch { return }
                guard !Task.isCancelled else { return }
                feedback = WorkspaceCopyFeedback()
            }
    }
    private var codeText: some View {
        Text(WorkspaceHighlight.applying(to: AttributedString(text), terms: terms))
            .font(.system(size: preferences.effectiveFontSize, design: .monospaced))
    }
}
