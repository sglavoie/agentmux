import Darwin
import XCTest
@testable import AgentMux

final class SubscriptionClientTests: XCTestCase {
    private var directory: URL!
    private var client: SubscriptionClient?
    private var server: FakeStreamServer?
    private let fast = ReconnectBackoff(initial: 0.05, maximum: 0.2)

    override func setUpWithError() throws {
        directory = URL(fileURLWithPath: "/tmp/amx-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        client?.stop()
        server?.shutdown()
        try? FileManager.default.removeItem(at: directory)
    }

    private func startClient(
        _ path: String,
        backoff: ReconnectBackoff? = nil,
        request: @escaping () -> SubscribeParams = { SubscribeParams(clientVersion: "test") }
    ) -> EventRecorder {
        let recorder = EventRecorder()
        let client = SubscriptionClient(
            socketPath: path,
            backoff: backoff ?? fast,
            request: request,
            onEvent: recorder.record
        )
        self.client = client
        client.start()
        return recorder
    }

    // MARK: Backoff

    func testBackoffDoublesFromHalfASecondToTen() {
        let backoff = ReconnectBackoff()
        XCTAssertEqual((0...6).map { backoff.delay(afterFailures: $0) }, [0.5, 1, 2, 4, 8, 10, 10])
        XCTAssertEqual(backoff.delay(afterFailures: 1000), 10)
    }

    // MARK: Live stream

    func testSubscribesSendsTheRequestAndDeliversFrames() throws {
        let received = Locked<String?>(nil)
        server = try FakeStreamServer(directory: directory, scripts: [{ conn in
            received.set(conn.readLine())
            conn.write(WireFixture.ack)
            conn.write(WireFixture.snapshot(seq: 1))
            conn.write("not json at all")
            conn.write(#"{"v":1,"seq":2,"type":"from_the_future","data":{"x":1}}"#)
            conn.write(WireFixture.heartbeat(seq: 3))
            Thread.sleep(forTimeInterval: 2)
            conn.close()
        }])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { $0.contains(.frame(.heartbeat(seq: 3))) })

        let events = recorder.events
        XCTAssertEqual(events.subscribedCount, 1)
        XCTAssertEqual(events.snapshotSeqs, [1])
        XCTAssertTrue(events.contains(.frame(.unknown(seq: 2, type: "from_the_future"))))
        XCTAssertEqual(client?.badLines, 1)

        let request = try XCTUnwrap(received.get())
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(request.utf8)) as? [String: Any])
        XCTAssertEqual(object["type"] as? String, "subscribe")
        let block = try XCTUnwrap(object["subscribe"] as? [String: Any])
        XCTAssertEqual(block["protocol"] as? Int, 1)
        XCTAssertEqual(block["client"] as? String, "Agent Mux.app")
        XCTAssertEqual(block["client_version"] as? String, "test")
    }

    func testByeReconnectsAndResubscribes() throws {
        server = try FakeStreamServer(directory: directory, scripts: [
            { conn in
                _ = conn.readLine()
                conn.write(WireFixture.ack)
                conn.write(WireFixture.snapshot(seq: 1))
                conn.write(WireFixture.bye(seq: 2))
                conn.close()
            },
            { conn in
                _ = conn.readLine()
                conn.write(WireFixture.ack)
                conn.write(WireFixture.snapshot(seq: 1, key: 2))
                Thread.sleep(forTimeInterval: 2)
                conn.close()
            },
        ])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 2 && $0.snapshotSeqs.count == 2 })
        let events = recorder.events
        XCTAssertEqual(events.disconnects.first, .bye("shutdown"))
        XCTAssertTrue(events.contains(.frame(.bye(seq: 2, reason: "shutdown"))))
        // A bye retries at the shortest delay.
        XCTAssertTrue(events.contains(.disconnected(.bye("shutdown"), retryIn: fast.initial)))
    }

    func testEOFWithoutByeReconnects() throws {
        server = try FakeStreamServer(directory: directory, scripts: [
            { conn in
                _ = conn.readLine()
                conn.write(WireFixture.ack)
                conn.close()
            },
            { conn in
                _ = conn.readLine()
                conn.write(WireFixture.ack)
                conn.write(WireFixture.snapshot(seq: 1))
                Thread.sleep(forTimeInterval: 2)
                conn.close()
            },
        ])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { $0.snapshotSeqs == [1] })
        guard case .lost = recorder.events.disconnects.first else {
            return XCTFail("disconnects = \(recorder.events.disconnects)")
        }
    }

    func testHeartbeatWatchdogCatchesASilentStream() throws {
        server = try FakeStreamServer(directory: directory, scripts: [
            { conn in
                _ = conn.readLine()
                // Heartbeats every 100ms are promised, and none come: the
                // socket stays open, as a half-open one would.
                conn.write(WireFixture.ack(heartbeatMS: 100))
                Thread.sleep(forTimeInterval: 3)
                conn.close()
            },
            { conn in
                _ = conn.readLine()
                conn.write(WireFixture.ack)
                Thread.sleep(forTimeInterval: 2)
                conn.close()
            },
        ])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 2 })
        XCTAssertEqual(recorder.events.disconnects.first, .heartbeatLost)
    }

    func testHeartbeatsKeepTheStreamAlive() throws {
        server = try FakeStreamServer(directory: directory, scripts: [{ conn in
            _ = conn.readLine()
            conn.write(WireFixture.ack(heartbeatMS: 200))
            for seq in 1...8 {
                Thread.sleep(forTimeInterval: 0.1)
                conn.write(WireFixture.heartbeat(seq: seq))
            }
            Thread.sleep(forTimeInterval: 2)
            conn.close()
        }])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { $0.contains(.frame(.heartbeat(seq: 8))) })
        XCTAssertEqual(recorder.events.disconnects, [])
    }

    func testOldDaemonIsReportedUnsupportedAndRetriedSlowly() throws {
        server = try FakeStreamServer(directory: directory, scripts: [{ conn in
            _ = conn.readLine()
            conn.write(#"{"ok":false,"error":"unknown request type: subscribe","ready":true}"#)
            conn.close()
        }])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { !$0.disconnects.isEmpty })
        XCTAssertEqual(recorder.events.first { if case .disconnected = $0 { return true } else { return false } },
                       .disconnected(.unsupported, retryIn: fast.maximum))
    }

    /// A snapshot this app cannot read is not skipped like any other bad
    /// line: the last good one would go on being shown as live. The stream
    /// ends, says why, and is dialed again at the slowest cadence.
    func testAnUnreadableSnapshotEndsTheStreamAndSaysSo() throws {
        server = try FakeStreamServer(directory: directory, scripts: [{ conn in
            _ = conn.readLine()
            conn.write(WireFixture.ack)
            conn.write(WireFixture.snapshot(seq: 1))
            conn.write(#"{"v":1,"seq":2,"type":"snapshot","data":["not","a","snapshot"]}"#)
            Thread.sleep(forTimeInterval: 2)
            conn.close()
        }])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { !$0.disconnects.isEmpty })
        XCTAssertEqual(recorder.events.snapshotSeqs, [1])
        XCTAssertEqual(recorder.events.first { if case .disconnected = $0 { return true } else { return false } },
                       .disconnected(.unreadableSnapshot, retryIn: fast.maximum))
        XCTAssertEqual(client?.badLines, 1)
    }

    func testNoSocketIsNotRunningAndBacksOff() {
        let recorder = startClient(directory.appendingPathComponent("absent.sock").path)
        XCTAssertTrue(recorder.wait { $0.disconnects.count >= 3 })
        XCTAssertEqual(Array(recorder.events.disconnects.prefix(3)), [.notRunning, .notRunning, .notRunning])
        let delays = recorder.events.compactMap { if case let .disconnected(_, delay) = $0 { return delay } else { return nil } }
        XCTAssertEqual(Array(delays.prefix(3)), [0.05, 0.1, 0.2])
    }

    /// After a run of failures the next dial is far off; asking for a
    /// reconnect (the app just started the daemon) dials at once and starts
    /// the backoff over, so the dials after it come quickly too.
    func testReconnectNowResetsTheBackoff() {
        let recorder = startClient(directory.appendingPathComponent("absent.sock").path,
                                   backoff: ReconnectBackoff(initial: 0.05, maximum: 30))
        XCTAssertTrue(recorder.wait { $0.disconnects.count >= 5 })
        let before = recorder.events.count
        let asked = Date()
        client?.reconnectNow()
        XCTAssertTrue(recorder.wait(timeout: 2) { !Array($0.dropFirst(before)).disconnects.isEmpty })
        XCTAssertLessThan(Date().timeIntervalSince(asked), 0.5, "the 0.8s wait was cut short")
        let after = Array(recorder.events.dropFirst(before))
        XCTAssertEqual(after.first, .connecting(attempt: 0))
        XCTAssertEqual(after.first { if case .disconnected = $0 { return true } else { return false } },
                       .disconnected(.notRunning, retryIn: 0.05))
    }

    func testReconnectNowDropsTheConnection() throws {
        server = try FakeStreamServer(directory: directory, scripts: [
            { conn in
                _ = conn.readLine()
                conn.write(WireFixture.ack)
                Thread.sleep(forTimeInterval: 3)
                conn.close()
            },
            { conn in
                _ = conn.readLine()
                conn.write(WireFixture.ack)
                Thread.sleep(forTimeInterval: 2)
                conn.close()
            },
        ])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 1 })
        client?.reconnectNow()
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 2 })
        XCTAssertEqual(recorder.events.disconnects, [.reconnectRequested])
    }

    /// The app's own request path: the build `resolveBinary` reports goes
    /// into `SubscribeRequestSource`, whose `params` the client sends, and a
    /// changed selection is what makes the app subscribe again.
    func testSubscribeReportsResolvedAMXBuild() throws {
        let daemonBinary = directory.appendingPathComponent("amx-daemon")
        let otherBinary = directory.appendingPathComponent("amx-other")
        try Data("the daemon's amx".utf8).write(to: daemonBinary)
        try Data("some other amx".utf8).write(to: otherBinary)
        let daemonHash = try XCTUnwrap(AMXBinaryResolver.sha256(daemonBinary.path))
        let version = try XCTUnwrap(AMXVersion.parse(
            "amx version 0.8.0 (revision 52189e529adf-dirty, built 2026-10-03T19:39:22Z, go1.27.1, sha256:\(daemonHash.prefix(12)))"
        ))

        // A build is only reported with the full content hash; the printed
        // prefix would never equal the daemon's.
        XCTAssertNil(AMXBuildReport.of(version: version, binaryHash: nil))
        XCTAssertNil(AMXBuildReport.of(binary: daemonBinary, version: nil))
        XCTAssertNil(AMXBuildReport.of(binary: directory.appendingPathComponent("missing"), version: version))
        let matching = try XCTUnwrap(AMXBuildReport.of(binary: daemonBinary, version: version))
        XCTAssertEqual(matching.binaryHash, daemonHash)
        XCTAssertEqual(matching.binaryHash.count, 64)
        let mismatching = try XCTUnwrap(AMXBuildReport.of(binary: otherBinary, version: version))
        XCTAssertNotEqual(mismatching.binaryHash, daemonHash)

        let source = SubscribeRequestSource(clientVersion: "test")
        XCTAssertTrue(source.report(build: matching))
        XCTAssertFalse(source.report(build: matching), "an unchanged selection needs no new subscribe")

        let requests = Locked<[String]>([])
        let script: (FakeConnection) -> Void = { conn in
            if let line = conn.readLine() { requests.set(requests.get() + [line]) }
            conn.write(WireFixture.ack)
            Thread.sleep(forTimeInterval: 3)
            conn.close()
        }
        server = try FakeStreamServer(directory: directory, scripts: [script, script, script])
        let recorder = startClient(server!.path, request: source.params)
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 1 })

        // The selection changes to another artifact, then to none at all.
        XCTAssertTrue(source.report(build: mismatching))
        client?.reconnectNow()
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 2 })
        XCTAssertTrue(source.report(build: nil))
        client?.reconnectNow()
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 3 })

        let blocks = try requests.get().map { line -> [String: Any] in
            let object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: Any])
            return try XCTUnwrap(object["subscribe"] as? [String: Any])
        }
        XCTAssertEqual(blocks.count, 3)
        let first = try XCTUnwrap(blocks[0]["amx_build"] as? [String: Any])
        XCTAssertEqual(first["binary_hash"] as? String, daemonHash)
        XCTAssertEqual(first["version"] as? String, "0.8.0")
        XCTAssertEqual(first["revision"] as? String, "52189e529adf")
        XCTAssertEqual(first["dirty"] as? Bool, true)
        XCTAssertEqual(first["build_time"] as? String, "2026-10-03T19:39:22Z")
        XCTAssertEqual(first["go_version"] as? String, "go1.27.1")
        let second = try XCTUnwrap(blocks[1]["amx_build"] as? [String: Any])
        XCTAssertEqual(second["binary_hash"] as? String, mismatching.binaryHash)
        // With nothing resolved the field is absent, the shape every daemon
        // since protocol 1 accepts and an older app always sent.
        XCTAssertNil(blocks[2]["amx_build"])
        XCTAssertEqual(blocks[2]["client_version"] as? String, "test")
    }

    func testStopEndsTheThreadPromptly() throws {
        server = try FakeStreamServer(directory: directory, scripts: [{ conn in
            _ = conn.readLine()
            conn.write(WireFixture.ack)
            Thread.sleep(forTimeInterval: 3)
            conn.close()
        }])
        let recorder = startClient(server!.path)
        XCTAssertTrue(recorder.wait { $0.subscribedCount == 1 })
        let started = Date()
        client?.stop()
        XCTAssertLessThan(Date().timeIntervalSince(started), 1)
        let count = recorder.events.count
        Thread.sleep(forTimeInterval: 0.4)
        XCTAssertEqual(recorder.events.count, count, "events after stop")
    }
}

/// A value shared between a fake server thread and the test.
final class Locked<T> {
    private let lock = NSLock()
    private var value: T
    init(_ value: T) { self.value = value }
    func get() -> T { lock.lock(); defer { lock.unlock() }; return value }
    func set(_ newValue: T) { lock.lock(); value = newValue; lock.unlock() }
}
