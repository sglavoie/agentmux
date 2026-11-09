import SwiftUI

/// Stable across launches and renames: the identity, not Swift's randomized hash,
/// determines a patch's constellation and color.
struct MissionPatchIdentity: Equatable {
    let seed: UInt64
    init(_ id: String) {
        seed = id.utf8.reduce(UInt64(14695981039346656037)) { ($0 ^ UInt64($1)) &* 1099511628211 }
    }
    var symbol: String { ["sparkles", "mountain.2.fill", "paperplane.fill", "moon.stars.fill", "bolt.fill", "sun.max.fill"][Int(seed % 6)] }
    var hue: Double { Double((seed >> 8) % 360) / 360 }
    var stars: Int { 3 + Int((seed >> 16) % 4) }
}

struct WorkspaceMissionPatch: View {
    let id: String
    let title: String
    let project: String
    let shippedAt: String
    var compact = false
    private var identity: MissionPatchIdentity { MissionPatchIdentity(id) }
    private var ink: Color { Color(hue: identity.hue, saturation: 0.55, brightness: 0.72) }
    var body: some View {
        VStack(spacing: compact ? 5 : 10) {
            ZStack {
                RoundedRectangle(cornerRadius: 24).fill(ink.gradient)
                RoundedRectangle(cornerRadius: 20).strokeBorder(.white.opacity(0.65), style: StrokeStyle(lineWidth: 1.5, dash: [3, 3])).padding(5)
                VStack(spacing: 7) {
                    Image(systemName: identity.symbol).font(.system(size: compact ? 24 : 36, weight: .medium))
                    HStack(spacing: 5) {
                        ForEach(0..<identity.stars, id: \.self) { _ in Image(systemName: "star.fill").font(.system(size: 5)) }
                    }
                }.foregroundStyle(.white)
            }.frame(width: compact ? 70 : 104, height: compact ? 80 : 118)
            if !compact {
                Text(title).font(.headline).multilineTextAlignment(.center).lineLimit(3)
                Text(project).font(.caption).foregroundStyle(.secondary)
                Text("Shipped " + (WorkspaceActivityEntry.date(shippedAt)?.formatted(date: .abbreviated, time: .omitted) ?? shippedAt))
                    .font(.caption2).foregroundStyle(.secondary)
            }
        }.frame(maxWidth: compact ? nil : .infinity)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel("\(title), \(project), mission patch. Shipped \(shippedAt)")
    }
}
