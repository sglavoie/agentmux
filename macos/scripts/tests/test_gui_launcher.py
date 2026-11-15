"""Exercise GUI job routing, status propagation and invocation-owned cleanup."""

import importlib.util
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).resolve().parents[1] / "run-in-gui.py"
SPEC = importlib.util.spec_from_file_location("gui_launcher", SCRIPT)
GUI = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GUI)


class GUILauncherTests(unittest.TestCase):
    def exercise(self, status, bootstrap_code=0, interruption=None, clock=None):
        calls = []
        jobs = []
        command = ["/path with spaces/test-app.sh", "Release", "literal $HOME; 'quote'"]

        def control(*args):
            calls.append(args)
            if args[0] == "bootstrap":
                path = Path(args[2])
                job = plistlib.loads(path.read_bytes())
                jobs.append((path, job))
                self.assertEqual(job["ProgramArguments"], command)
                self.assertEqual(job["WorkingDirectory"], os.getcwd())
                self.assertEqual(job["EnvironmentVariables"], dict(os.environ))
                self.assertEqual(path.parent.stat().st_mode & 0o777, 0o700)
                return subprocess.CompletedProcess(args, bootstrap_code, "", "denied" if bootstrap_code else "")
            if args[0] == "print" and "/io.github" in args[1]:
                if interruption:
                    raise interruption
                return subprocess.CompletedProcess(args, 0, status, "")
            return subprocess.CompletedProcess(args, 0, "", "")

        with patch.object(GUI, "launchctl", side_effect=control), \
             patch.object(GUI.time, "sleep"), \
             patch.object(GUI.time, "monotonic", side_effect=clock or [0, 31]):
            try:
                return GUI.run(command)
            finally:
                self.assertEqual(len(jobs), 1)
                path, job = jobs[0]
                self.assertFalse(path.parent.exists(), "Temporary environment and output files must be removed")
                cleanup = [call for call in calls if call[0] == "bootout"]
                expected = [] if bootstrap_code else [("bootout", f"gui/{os.getuid()}/{job['Label']}")]
                self.assertEqual(cleanup, expected, "Cleanup must target only this invocation's loaded job")

    def test_success_and_failure_status_with_launchd_annotations(self):
        for code in (0, 65):
            with self.subTest(code=code):
                self.assertEqual(self.exercise(f"\tstate = not running\n\tlast exit code = {code}: EX_DATAERR\n"), code)

    def test_signal_status(self):
        for description in ("15", "Terminated: 15"):
            with self.subTest(description=description):
                self.assertEqual(self.exercise(f"\tstate = not running\n\tlast terminating signal = {description}\n"), 143)

    def test_bootstrap_failure_cleans_files_without_unloading_another_job(self):
        with self.assertRaisesRegex(RuntimeError, "Cannot start tests.*denied"):
            self.exercise("", bootstrap_code=1)

    def test_interruption_unloads_job_and_cleans_files(self):
        with self.assertRaises(SystemExit) as result:
            self.exercise("", interruption=SystemExit(143))
        self.assertEqual(result.exception.code, 143)

    def test_job_that_never_starts_fails_and_cleans_up(self):
        with self.assertRaisesRegex(RuntimeError, "did not start"):
            self.exercise("\tstate = spawn scheduled\n")

    def test_missing_gui_login_is_actionable_and_does_not_launch(self):
        with patch.object(GUI, "launchctl", return_value=subprocess.CompletedProcess([], 1, "", "")) as control:
            with self.assertRaisesRegex(RuntimeError, "Log in to this Mac's desktop"):
                GUI.run(["/bin/true"])
        control.assert_called_once_with("print", f"gui/{os.getuid()}")


class TestAppRoutingTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.scripts = self.root / "scripts"
        self.scripts.mkdir()
        shutil.copyfile(SCRIPT.with_name("test-app.sh"), self.scripts / "test-app.sh")
        self.env = dict(os.environ, PATH=f"{self.scripts}:{os.environ['PATH']}", CALLS=str(self.root / "calls"))
        self.tool("remote-ui.sh", '''if [ "$1" = mode ]; then
    echo "${ROUTE:-local}"
else
    printf 'remote:%s\\n' "$*" >> "$CALLS"
    exit 23
fi
''')
        self.tool("launchctl", 'echo "${MANAGER:-Aqua}"\n')
        self.tool("python3", 'printf "gui:%s\\n" "$*" >> "$CALLS"; exit 17\n')
        self.tool("generate-project.sh", 'echo generated >> "$CALLS"\n')
        self.tool("signing-lib.sh", 'xcode_signing_settings() { echo "CODE_SIGN_IDENTITY=fixture DEVELOPMENT_TEAM=TEAM"; }\n')
        self.tool("xcodebuild", '''printf 'xcode:%s\\n' "$*" >> "$CALLS"
echo "Executed ${TEST_COUNT:-10} tests, with 0 failures"
exit "${XCODE_STATUS:-0}"
''')

    def tool(self, name, body):
        path = self.scripts / name
        path.write_text("#!/bin/sh\n" + body)
        path.chmod(0o755)

    def run_script(self, *args, **environment):
        result = subprocess.run(["/bin/sh", str(self.scripts / "test-app.sh"), *args],
                                env=self.env | environment, capture_output=True, text=True)
        calls = self.root / "calls"
        return result, calls.read_text() if calls.exists() else ""

    def test_remote_routing_precedes_gui_dispatch(self):
        result, calls = self.run_script("Release", ROUTE="remote", MANAGER="Background")
        self.assertEqual(result.returncode, 23)
        self.assertEqual(calls, "remote:run macos/scripts/test-app.sh Release\n")

    def test_remote_only_refusal_never_dispatches_locally(self):
        result, calls = self.run_script(ROUTE="refuse", MANAGER="Background")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(calls, "")

    def test_background_dispatch_forwards_configuration_and_status(self):
        result, calls = self.run_script("Release", MANAGER="Background")
        self.assertEqual(result.returncode, 17)
        self.assertIn(f"gui:{self.scripts}/run-in-gui.py {self.scripts}/test-app.sh Release", calls)
        self.assertNotIn("generated", calls)

    def test_aqua_preserves_release_signing_arguments(self):
        result, calls = self.run_script("Release")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("-configuration Release", calls)
        self.assertIn("ENABLE_TESTABILITY=YES CODE_SIGN_IDENTITY=fixture DEVELOPMENT_TEAM=TEAM", calls)
        self.assertNotIn("gui:", calls)

    def test_zero_tests_still_fails(self):
        result, _ = self.run_script(TEST_COUNT="0")
        self.assertEqual(result.returncode, 1)
        self.assertIn("no test was executed", result.stderr)

    def test_xcode_failure_status_is_preserved(self):
        result, _ = self.run_script(XCODE_STATUS="65")
        self.assertEqual(result.returncode, 65)


if __name__ == "__main__":
    unittest.main()
