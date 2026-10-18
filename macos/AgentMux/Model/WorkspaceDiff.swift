import Foundation

/// Unified diff rows retain the original prefix and separate old/new line
/// numbers. File headers and no-newline markers never consume source lines.
struct WorkspaceDiff: Equatable {
    enum Kind { case metadata, hunk, added, removed, context }
    struct Line: Equatable {
        let text: String
        let kind: Kind
        let old: Int?
        let new: Int?
    }
    let patch: String
    let lines: [Line]
    let hunks: [Int]
    let changedWords: [Int: [NSRange]]

    init(_ patch: String) {
        let header = try! NSRegularExpression(pattern: #"^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@"#)
        var lines: [Line] = []
        var hunks: [Int] = []
        var old = 0, new = 0
        var inHunk = false
        for text in patch.components(separatedBy: "\n") {
            if let match = header.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)),
               let a = Range(match.range(at: 1), in: text), let b = Range(match.range(at: 2), in: text) {
                old = Int(text[a]) ?? 0; new = Int(text[b]) ?? 0
                inHunk = true
                hunks.append(lines.count)
                lines.append(Line(text: text, kind: .hunk, old: nil, new: nil))
            } else if text.hasPrefix("diff ") {
                inHunk = false
                lines.append(Line(text: text, kind: .metadata, old: nil, new: nil))
            } else if inHunk && text.hasPrefix("+") {
                lines.append(Line(text: text, kind: .added, old: nil, new: new)); new += 1
            } else if inHunk && text.hasPrefix("-") {
                lines.append(Line(text: text, kind: .removed, old: old, new: nil)); old += 1
            } else if inHunk && text.hasPrefix(" ") {
                lines.append(Line(text: text, kind: .context, old: old, new: new)); old += 1; new += 1
            } else {
                lines.append(Line(text: text, kind: .metadata, old: nil, new: nil))
            }
        }
        self.patch = patch
        self.lines = lines
        self.hunks = hunks
        self.changedWords = Self.changedWords(in: lines)
    }

    /// Only hide redundant file headers, never mode/rename/binary/no-newline evidence.
    func redundantHeader(_ index: Int) -> Bool {
        guard !hunks.isEmpty, lines[index].kind == .metadata else { return false }
        return ["diff --git ", "index ", "--- ", "+++ "].contains { lines[index].text.hasPrefix($0) }
    }

    private static func changedWords(in lines: [Line]) -> [Int: [NSRange]] {
        var result: [Int: [NSRange]] = [:]
        var index = 0, budget = 1_000_000
        while index < lines.count && budget > 0 {
            guard lines[index].kind == .removed else { index += 1; continue }
            let start = index
            while index < lines.count && lines[index].kind == .removed { index += 1 }
            let added = index
            while index < lines.count && lines[index].kind == .added { index += 1 }
            // Unequal blocks have no reliable line pairing: retain whole-line shading.
            guard added - start == index - added, added - start <= 100 else { continue }
            for offset in 0..<(added - start) {
                guard budget > 0 else { break }
                let old = tokens(lines[start + offset].text), new = tokens(lines[added + offset].text)
                guard !old.isEmpty, !new.isEmpty else { continue }
                let cost = old.count * new.count
                guard cost <= budget else { continue }
                budget -= cost
                let changes = new.map(\.text).difference(from: old.map(\.text))
                for change in changes {
                    switch change {
                    case let .remove(position, _, _): result[start + offset, default: []].append(old[position].range)
                    case let .insert(position, _, _): result[added + offset, default: []].append(new[position].range)
                    }
                }
            }
        }
        return result
    }

    private struct Token { var text: String; var range: NSRange; let kind: Int }
    private static func tokens(_ line: String) -> [Token] {
        guard line.utf16.count <= 8192 else { return [] }
        var tokens: [Token] = [], offset = 1
        for character in line.dropFirst() {
            let text = String(character), length = text.utf16.count
            let kind = character.isLetter || character.isNumber || character == "_" ? 0 : character.isWhitespace ? 1 : 2
            if kind != 2, tokens.last?.kind == kind {
                tokens[tokens.count - 1].text += text
                tokens[tokens.count - 1].range.length += length
            } else {
                tokens.append(Token(text: text, range: NSRange(location: offset, length: length), kind: kind))
                if tokens.count > 256 { return [] }
            }
            offset += length
        }
        return tokens
    }
}
