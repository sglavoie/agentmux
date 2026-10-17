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
    }
}
