"""Evidence rejection regressions; synthetic fixtures are not visual reviews."""

import copy
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
from fractions import Fraction
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location(
    "desktop_review", Path(__file__).resolve().parents[1] / "check-desktop-review.py")
reviewer = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reviewer)


class DesktopReviewTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.review = {"schema_version": 1, "reviewer": "Synthetic test fixture",
                       "reviewed_at": "2026-11-16T00:00:00Z", "app_source_revision": "a" * 40,
                       "artifacts": []}
        for path in reviewer.ARTIFACTS:
            content = ("synthetic bytes: " + path).encode()
            target = self.root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
            fps = 10 if path.endswith(".gif") else 30
            self.review["artifacts"].append({
                "path": path, "old_sha256": "0" * 64,
                "new_sha256": hashlib.sha256(content).hexdigest(), "byte_size": len(content),
                "frame_rate": f"{fps}/1", "scenes": [
                    {"id": scene, "timestamp_seconds": i, "frame_index": i * fps,
                     "result": "pass", "observations": "Synthetic test observation"}
                    for i, scene in enumerate(reviewer.SCENES)]})
        self.probe = patch.object(reviewer, "probe", side_effect=self.timing)
        self.probe.start()
        self.addCleanup(self.probe.stop)

    @staticmethod
    def timing(path):
        fps = 10 if path.suffix == ".gif" else 30
        return Fraction(fps), Fraction(fps), 7.0, [i / fps for i in range(7 * fps)]

    def test_complete_evidence(self):
        self.assertEqual(reviewer.validate(self.review, self.root), 2)

    def test_rejects_missing_metadata(self):
        for field in ("schema_version", "reviewer", "reviewed_at", "app_source_revision", "artifacts"):
            with self.subTest(field=field):
                review = copy.deepcopy(self.review)
                del review[field]
                with self.assertRaises(ValueError):
                    reviewer.validate(review, self.root)

    def test_rejects_invalid_metadata(self):
        for field, value in (("schema_version", True), ("reviewer", " "),
                             ("reviewed_at", "2026-11-16"), ("app_source_revision", "HEAD")):
            with self.subTest(field=field):
                review = copy.deepcopy(self.review)
                review[field] = value
                with self.assertRaises(ValueError):
                    reviewer.validate(review, self.root)

    def test_rejects_stale_or_unmeasured_artifacts(self):
        for index in range(2):
            original = self.review["artifacts"][index]
            cases = (("old_sha256", original["new_sha256"]), ("new_sha256", "f" * 64),
                     ("old_sha256", "bad"), ("byte_size", original["byte_size"] + 1),
                     ("byte_size", True), ("frame_rate", "5/1"),
                     ("path", self.review["artifacts"][1 - index]["path"]))
            for field, value in cases:
                with self.subTest(artifact=index, field=field):
                    review = copy.deepcopy(self.review)
                    review["artifacts"][index][field] = value
                    with self.assertRaises(ValueError):
                        reviewer.validate(review, self.root)

    def test_rejects_changed_bytes(self):
        for path in reviewer.ARTIFACTS:
            with self.subTest(path=path):
                target = self.root / path
                original = target.read_bytes()
                target.write_bytes(b"changed")
                with self.assertRaisesRegex(ValueError, "hash mismatch"):
                    reviewer.validate(self.review, self.root)
                target.write_bytes(original)

    def test_rejects_bad_scenes(self):
        cases = (("id", "unknown"), ("timestamp_seconds", -0.001),
                 ("timestamp_seconds", 7), ("timestamp_seconds", float("nan")),
                 ("timestamp_seconds", True), ("timestamp_seconds", 0),
                 ("frame_index", 0), ("frame_index", 10000),
                 ("frame_index", True), ("frame_index", 1),
                 ("result", "fail"), ("observations", "  "), ("observations", []))
        for index in range(2):
            for field, value in cases:
                with self.subTest(artifact=index, field=field, value=value):
                    review = copy.deepcopy(self.review)
                    review["artifacts"][index]["scenes"][1][field] = value
                    with self.assertRaises(ValueError):
                        reviewer.validate(review, self.root)
            for change in (lambda scenes: scenes.pop(), lambda scenes: scenes.reverse(),
                           lambda scenes: scenes.append(scenes[-1])):
                review = copy.deepcopy(self.review)
                change(review["artifacts"][index]["scenes"])
                with self.assertRaises(ValueError):
                    reviewer.validate(review, self.root)

    def test_requires_measured_output_rates(self):
        for fps in (9, 15):
            with self.subTest(fps=fps):
                review = copy.deepcopy(self.review)
                for artifact in review["artifacts"]:
                    artifact["frame_rate"] = f"{fps}/1"
                with patch.object(reviewer, "probe", return_value=(Fraction(fps), Fraction(fps), 7,
                                                                 [i / fps for i in range(7 * fps)])):
                    with self.assertRaises(ValueError):
                        reviewer.validate(review, self.root)

    def test_requires_nominal_mp4_rate(self):
        def timing(path):
            rate, nominal, duration, frames = self.timing(path)
            return rate, Fraction(60) if path.suffix == ".mp4" else nominal, duration, frames
        with patch.object(reviewer, "probe", side_effect=timing):
            with self.assertRaisesRegex(ValueError, "MP4 must be 30 fps"):
                reviewer.validate(self.review, self.root)


if __name__ == "__main__":
    unittest.main()
