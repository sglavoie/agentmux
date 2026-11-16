import CoreML
import Vision
import XCTest

/// The guest lacks the ANE backend used by accurate OCR, including when its
/// request advertises CPU support. Fast OCR has a CPU implementation. Keep every
/// rendered-text assertion; physical Macs retain their existing accurate mode.
@available(macOS 14.0, *)
func useCPUForTestOCR(_ request: VNRecognizeTextRequest) throws {
    guard ProcessInfo.processInfo.environment["AMX_TEST_OCR_MODE"] == "cpu-fast" else { return }
    request.recognitionLevel = .fast
    for (stage, devices) in try request.supportedComputeStageDevices {
        let cpu = try XCTUnwrap(devices.first {
            if case .cpu = $0 { return true }
            return false
        }, "OCR compute stage must support CPU execution: \(stage)")
        request.setComputeDevice(cpu, for: stage)
    }
}
