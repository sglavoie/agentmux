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

    private func startClient(_ path: String, backoff: ReconnectBackoff? = nil) -> EventRecorder {
        let recorder = EventRecorder()
        let client = SubscriptionClient(
            socketPath: path,
            backoff: backoff ?? fast,
            request: { SubscribeParams(clientVersion: "test") },
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

    func testNoSocketIsNotRunningAndBacksOff() {
        let recorder = startClient(directory.appendingPathComponent("absent.sock").path)
        XCTAssertTrue(recorder.wait { $0.disconnects.count >= 3 })
        XCTAssertEqual(Array(recorder.events.disconnects.prefix(3)), [.notRunning, .notRunning, .notRunning])
        let delays = recorder.events.compactMap { if case let .disconnected(_, delay) = $0 { return delay } else { return nil } }
        XCTAssertEqual(Array(delays.prefix(3)), [0.05, 0.1, 0.2])
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
