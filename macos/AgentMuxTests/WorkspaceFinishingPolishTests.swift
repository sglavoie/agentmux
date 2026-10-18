import AppKit
import SwiftUI
import XCTest
import Darwin
@testable import AgentMux

private struct FinishingRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}
private final class FinishingService: ASLService {
    var result = ASLResult(status: ASLStatus(available: true, mutationsEnabled: true), snapshot: ASLSnapshot(), meta: ASLSnapshotMeta())
    var reads = 0
    func snapshot() -> Result<ASLResult, ASLRequestError> { reads += 1; return .success(result) }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> { .failure(.unsupported) }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> { XCTFail("Refresh must not perform an action"); return .failure(.unsupported) }
}

@MainActor
final class WorkspaceFinishingPolishTests: XCTestCase {
    func testReadFailureTakesPrecedenceOverRetainedContentAndSuccess() {
        let failed = WorkspaceReadObservation(title: "Diff", retained: true, feedback: .init(target: "Diff", message: "Unavailable"))
        let success = WorkspaceReadObservation(title: "Comparison", feedback: .init(target: "Comparison", message: "Copied", kind: .success))
        XCTAssertEqual(WorkspaceReviewStatus(reads: [success, failed]).label, "Refresh failed")
        XCTAssertTrue(failed.description.contains("Previous results"))
    }

    private func row() -> ASLRow {
        var row = ASLRow()
        row.rowID = "row"; row.runID = "run"; row.planDir = "/plan"; row.sessionID = "9"
        row.primaryState = "paused"; row.pauseReason = "verification-failed"
        row.actions = [ASLMethod.runResume, ASLMethod.runAbort]; row.actionToken = "token"
        return row
    }

    func testRunActionPriorityAndPendingExplanation() {
        let service = FinishingService(); service.result.snapshot?.rows = [row()]
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: FinishingRunner())
        model.refresh()
        XCTAssertEqual(model.nextAction, .inspect)
        XCTAssertEqual(model.sessionStatus(.init(id: "9", status: "pending", path: "/session")), "Incomplete · verification failed")
        XCTAssertEqual(model.sessionStatus(.init(id: "10", status: "pending", path: "/later")), "Pending")
        service.result.snapshot?.rows[0].question = ASLRowQuestion(questionID: "question")
        model.refresh(); XCTAssertEqual(model.nextAction, .answer)
        XCTAssertNotNil(model.disabledReason(ASLMethod.runResume))
        service.result.snapshot?.rows[0].question = nil
        service.result.snapshot?.rows[0].pauseReason = "operator-paused"
        model.refresh(); XCTAssertEqual(model.nextAction, .resume)
        service.result.snapshot?.rows[0].pauseReason = "verification-pending"
        model.refresh()
        XCTAssertFalse(model.verificationBlocked, "Unknown verification reasons must not be relabeled as failures")
        XCTAssertEqual(model.sessionStatus(.init(id: "9", status: "pending", path: "/session")), "Pending")
        service.result.status.snapshotStale = true
        model.refresh(); XCTAssertEqual(model.nextAction, .focus)
        XCTAssertNotNil(model.disabledReason(ASLMethod.runResume))
    }

    func testAutomaticReadsAreBoundedAndResumeImmediatelyWithoutMutations() {
        let service = FinishingService(); service.result.snapshot?.rows = [row()]
        var now = Date(timeIntervalSince1970: 1000), calls = 0
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, _, _, _ in
            calls += 1
            let json = #"{"schema_version":1,"data":{"plan_dir":"/plan","plan_title":"Plan","counts":{"pending":1},"sessions":[],"runner_state":{"run_id":"run","paused":true,"reason":"verification-failed","run_started_at":"","run_finished_at":""},"verification":[],"verification_truncated":false}}"#
            return .finished(.init(status: 0, stdout: Data(json.utf8), stderr: Data()))
        }
        let model = WorkspaceRunsModel(service: service, command: { command }, runner: FinishingRunner(), now: { now })
        model.start(); defer { model.close() }
        XCTAssertEqual(calls, 1)
        let originalRead = model.detailRefreshedAt
        for tick in 1...11 {
            now = Date(timeIntervalSince1970: 1000 + Double(tick * 5))
            service.result.snapshot?.rows[0].actionToken = "token-\(tick)"
            model.automaticRefresh()
        }
        XCTAssertEqual(service.reads, 12)
        XCTAssertEqual(calls, 2, "One initial process and one 30-second fallback, instead of 12 processes")
        XCTAssertNotEqual(model.detailRefreshedAt, originalRead)
        let lastRead = model.detailRefreshedAt
        model.setAutomaticRefreshEnabled(false)
        for _ in 0..<60 { now += 5; model.automaticRefresh() }
        XCTAssertEqual(service.reads, 12); XCTAssertEqual(calls, 2)
        XCTAssertEqual(model.detailRefreshedAt, lastRead, "Suspending cannot make retained results look fresh")
        model.setAutomaticRefreshEnabled(true)
        XCTAssertEqual(service.reads, 13); XCTAssertEqual(calls, 3)
        service.result.snapshot?.rows[0].lastActivityAt = "changed"
        model.automaticRefresh(); XCTAssertEqual(calls, 4, "A changed summary is read on the next tick")
        model.refresh(); XCTAssertEqual(calls, 5, "Explicit refresh always reads details")
        model.close(); model.automaticRefresh(); XCTAssertEqual(calls, 5)
    }

    func testCompactDiffKeepsEvidenceUnicodeAndRawPatch() throws {
        let patch = "diff --git a/a b/a\nindex 123..456 100644\nold mode 100644\nnew mode 100755\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-let café = oldValue(🦊, 12)\n+let café = newValue(🦊, 24)\n\\ No newline at end of file\n"
        let diff = WorkspaceDiff(patch)
        let rendered = WorkspaceDiffView.render(diff, compactMetadata: true)
        XCTAssertFalse(rendered.text.string.contains("diff --git"))
        XCTAssertFalse(rendered.text.string.contains("+++ b/a"))
        XCTAssertTrue(rendered.text.string.contains("new mode 100755"))
        XCTAssertTrue(rendered.text.string.contains("No newline"))
        XCTAssertEqual(diff.patch, patch)
        XCTAssertEqual(WorkspaceDiffView.render(diff).text.string, patch)
        let added = try XCTUnwrap(diff.lines.firstIndex { $0.kind == .added })
        let words = (diff.changedWords[added] ?? []).map { (diff.lines[added].text as NSString).substring(with: $0) }
        XCTAssertEqual(Set(words), ["newValue", "24"])
        let text = WorkspaceDiffTextView()
        text.diff = diff; text.lineRanges = rendered.lines; text.textStorage?.setAttributedString(rendered.text)
        XCTAssertEqual(text.sourceText(in: (text.string as NSString).range(of: "café = newValue(🦊, 24)")), "café = newValue(🦊, 24)")
        XCTAssertEqual(rendered.lines.count, diff.lines.count)
        XCTAssertEqual((rendered.text.string as NSString).substring(with: rendered.hunks[0]), "@@ -1 +1 @@")
        let binary = WorkspaceDiff("diff --git a/image b/image\nBinary files a/image and b/image differ\n")
        XCTAssertEqual(WorkspaceDiffView.render(binary, compactMetadata: true).text.string, binary.patch)
    }

    func testIntralineWorkIsBoundedAndDoesNotInventUnequalPairings() {
        XCTAssertTrue(WorkspaceDiff("@@ -1 +1,2 @@\n-old\n+new\n+extra").changedWords.isEmpty)
        let long = String(repeating: "x", count: 10_000)
        XCTAssertTrue(WorkspaceDiff("@@ -1 +1 @@\n-\(long)1\n+\(long)2").changedWords.isEmpty)
        let patch = "@@ -1 +1 @@\n-let é = 👩🏽‍💻\n+let é = 🧑🏽‍💻"
        let diff = WorkspaceDiff(patch)
        XCTAssertEqual(diff.changedWords[2]?.map { (diff.lines[2].text as NSString).substring(with: $0) }, ["🧑🏽‍💻"])
    }

    func testRealStatusProcessCountsLatencyAndSuspendedCPU() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("amx-refresh-\(UUID())")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let script = directory.appendingPathComponent("amx")
        let json = #"{"schema_version":1,"data":{"plan_dir":"/plan","plan_title":"Plan","counts":{"pending":1},"sessions":[],"runner_state":{"run_id":"run","paused":true,"reason":"verification-failed","run_started_at":"","run_finished_at":""},"verification":[],"verification_truncated":false}}"#
        try ("#!/bin/sh\nprintf x >> \"$0.calls\"\nprintf '%s' '" + json + "'\n").write(to: script, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: script.path)
        let command = AMXCommand(binary: script, launchEnvironment: LaunchEnvironment(home: directory.path, path: "/usr/bin:/bin"))
        let service = FinishingService(); service.result.snapshot?.rows = [row()]
        var now = Date(timeIntervalSince1970: 1000)
        let model = WorkspaceRunsModel(service: service, command: { command }, runner: FinishingRunner(), now: { now })
        func calls() throws -> Int { try Data(contentsOf: URL(fileURLWithPath: script.path + ".calls")).count }
        model.start(); defer { model.close() }
        for _ in 0..<11 { now += 5; model.automaticRefresh() }
        XCTAssertEqual(try calls(), 2)
        let began = ProcessInfo.processInfo.systemUptime
        service.result.snapshot?.rows[0].lastActivityAt = "changed"
        model.automaticRefresh()
        let latency = ProcessInfo.processInfo.systemUptime - began
        XCTAssertEqual(try calls(), 3)
        XCTAssertNotNil(model.detail)
        model.setAutomaticRefreshEnabled(false)
        let reads = service.reads
        func cpu() -> Double {
            var usage = rusage(); getrusage(RUSAGE_SELF, &usage)
            return Double(usage.ru_utime.tv_sec + usage.ru_stime.tv_sec) + Double(usage.ru_utime.tv_usec + usage.ru_stime.tv_usec) / 1_000_000
        }
        let cpuBefore = cpu()
        try await Task.sleep(for: .milliseconds(5200))
        let cpuUsed = cpu() - cpuBefore
        XCTAssertEqual(service.reads, reads, "The suspended timer must make no daemon reads")
        XCTAssertEqual(try calls(), 3, "The suspended timer must spawn no status processes")
        print("RUN_REFRESH_MEASUREMENT: 2 status processes / 12 unchanged snapshots; changed-row read \(latency * 1000) ms; suspended host CPU \(cpuUsed * 1000) ms / 5200 ms wall (whole XCTest host)")
    }

    func testVisibleHunkTracksScrollingAndJumpWithoutChangingRawCopy() async throws {
        let patch = "diff --git a/file b/file\n--- a/file\n+++ b/file\n@@ -1,40 +1,40 @@\n" + (1...40).map { " line \($0)\n" }.joined() + "@@ -80 +80 @@\n-old value\n+new value\n"
        let diff = WorkspaceDiff(patch)
        var visible: Int?
        func view(jump: Int?, sequence: Int) -> WorkspaceDiffView {
            WorkspaceDiffView(diff: diff, identity: "file", loading: false, jump: jump, jumpSequence: sequence, onVisibleHunk: { visible = $0 })
        }
        let host = NSHostingView(rootView: view(jump: nil, sequence: 0))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 250), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false; window.contentView = host; window.orderFront(nil)
        defer { window.close() }
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(visible, 0)
        host.rootView = view(jump: 1, sequence: 1)
        try await Task.sleep(for: .milliseconds(100))
        func descendants(_ view: NSView) -> [NSView] { [view] + view.subviews.flatMap(descendants) }
        let text = try XCTUnwrap(descendants(host).compactMap { $0 as? WorkspaceDiffTextView }.first)
        // Put the second heading at the top, as manual scrolling would.
        let y = try XCTUnwrap(text.lineRect(diff.hunks[1])).minY
        text.enclosingScrollView?.contentView.scroll(to: NSPoint(x: 0, y: y))
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(visible, 1)
        XCTAssertEqual(text.diff.patch, patch)
        text.enclosingScrollView?.contentView.scroll(to: .zero)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(visible, 0)
    }
}
