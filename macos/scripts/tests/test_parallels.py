"""Guest-only regressions for at-most-once Parallels execution and real exits."""

import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location(
    "parallels", Path(__file__).resolve().parents[1] / "parallels.py")
ROUTE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ROUTE)
RUN = subprocess.run


class ParallelsTransportTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="parallels route ")
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)

    def script(self, code=0):
        return ROUTE.write_guest_script(
            f"echo ran >> executions; printf payload; printf diagnostic >&2; exit {code}",
            self.directory, self.directory)

    def transport_error(self):
        return subprocess.CompletedProcess([], 255, b"", b"PrlJob_GetResult: Invalid argument")

    def invoke_script(self, script):
        return RUN(["/bin/sh", str(script)], capture_output=True)

    def test_prelaunch_failure_retries_once_and_preserves_test_failure(self):
        script = self.script(65)
        outcomes = [self.transport_error(), None]

        def launch(*args, **kwargs):
            return outcomes.pop(0) or self.invoke_script(script)

        with patch.object(ROUTE.subprocess, "run", side_effect=launch) as launch_mock, \
             patch.object(ROUTE.time, "sleep"):
            self.assertEqual(ROUTE.execute_guest("macOS", self.directory, self.directory), 65)
        self.assertEqual(launch_mock.call_count, 2)
        self.assertEqual((self.directory / "transport-1.status").read_text(), "255\n")
        self.assertIn("Invalid argument", (self.directory / "transport-1.stderr").read_text())
        self.assertEqual((self.directory / "transport-2.status").read_text(), "65\n")
        self.assertEqual((self.directory / "executions").read_text(), "ran\n")
        self.assertEqual((self.directory / "stdout").read_text(), "payload")
        self.assertEqual((self.directory / "stderr").read_text(), "diagnostic")

    def test_receipt_preserves_exit_after_transport_loses_result(self):
        for code in (0, 65, 255):
            with self.subTest(code=code), tempfile.TemporaryDirectory() as folder:
                directory = Path(folder)
                script = ROUTE.write_guest_script(f"exit {code}", directory, directory)

                def launch(*args, **kwargs):
                    self.invoke_script(script)
                    return self.transport_error()

                with patch.object(ROUTE.subprocess, "run", side_effect=launch) as mocked:
                    self.assertEqual(ROUTE.execute_guest("macOS", directory, directory), code)
                self.assertEqual(mocked.call_count, 1)

    def test_duplicate_launch_cannot_execute_payload_twice(self):
        script = self.script()
        self.assertEqual(self.invoke_script(script).returncode, 0)
        self.assertEqual(self.invoke_script(script).returncode, 125)
        self.assertEqual((self.directory / "executions").read_text(), "ran\n")
        self.assertEqual((self.directory / "result").read_text(), "0\n")

    def test_started_without_result_is_never_retried(self):
        (self.directory / "claimed").mkdir()
        with patch.object(ROUTE.subprocess, "run", return_value=self.transport_error()) as mocked:
            with self.assertRaisesRegex(RuntimeError, "started.*NOT retried"):
                ROUTE.execute_guest("macOS", self.directory, self.directory)
        self.assertEqual(mocked.call_count, 1)

    def test_exhausted_prelaunch_retries_fail_closed(self):
        with patch.object(ROUTE.subprocess, "run", return_value=self.transport_error()) as mocked, \
             patch.object(ROUTE.time, "sleep"):
            with self.assertRaisesRegex(RuntimeError, "did not publish"):
                ROUTE.execute_guest("macOS", self.directory, self.directory, attempts=3)
        self.assertEqual(mocked.call_count, 3)
        self.assertFalse((self.directory / "claimed").exists())

    def test_unknown_transport_error_and_missing_success_receipt_fail_closed(self):
        for code in (0, 1):
            with self.subTest(code=code), patch.object(
                    ROUTE.subprocess, "run", return_value=subprocess.CompletedProcess([], code, b"", b"unknown")) as mocked:
                with self.assertRaisesRegex(RuntimeError, "did not publish"):
                    ROUTE.execute_guest("macOS", self.directory, self.directory)
                self.assertEqual(mocked.call_count, 1)


if __name__ == "__main__":
    unittest.main()
