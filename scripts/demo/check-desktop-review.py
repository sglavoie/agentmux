#!/usr/bin/env python3
"""Check encoded desktop review freshness/completeness, not visual truth.

Paths are relative to the repository, regardless of the caller's working directory.
Requires Python 3 and ffprobe. See scripts/demo/README.md for the evidence schema.
"""

import argparse
from datetime import datetime
from fractions import Fraction
import hashlib
import json
import math
from pathlib import Path
import re
import subprocess
import sys

REPO = Path(__file__).resolve().parents[2]
ARTIFACTS = ("docs/media/desktop-demo.gif", "docs/media/desktop-demo.mp4")
SCENES = ("story-lines", "needs-attention", "first-option", "second-option",
          "all-caught-up", "briefing-collapsed", "checkout-review")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def nonempty(value):
    return isinstance(value, str) and bool(value.strip())


def digest(value):
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) is not None


def number(value):
    return type(value) in (int, float) and math.isfinite(value)


def probe(path):
    result = subprocess.run([
        "ffprobe", "-v", "error", "-select_streams", "v:0",
        "-show_entries", "stream=avg_frame_rate,r_frame_rate:frame=best_effort_timestamp_time,pkt_duration_time:format=duration",
        "-show_frames", "-of", "json", str(path),
    ], check=True, capture_output=True, text=True, timeout=45)
    data = json.loads(result.stdout)
    require(len(data.get("streams", [])) == 1, f"{path}: missing video stream")
    stream = data["streams"][0]
    rate = Fraction(stream["avg_frame_rate"])
    nominal = Fraction(stream["r_frame_rate"])
    duration = float(data["format"]["duration"])
    frames = [float(frame["best_effort_timestamp_time"]) for frame in data["frames"]]
    require(rate > 0 and math.isfinite(duration) and duration > 0 and frames,
            f"{path}: invalid video timing")
    require(all(math.isfinite(t) and 0 <= t < duration for t in frames),
            f"{path}: invalid frame timestamps")
    require(all(a < b for a, b in zip(frames, frames[1:])),
            f"{path}: frame timestamps must increase")
    return rate, nominal, duration, frames


def validate(review, root=REPO):
    require(isinstance(review, dict), "review must be an object")
    require(type(review.get("schema_version")) is int and review["schema_version"] == 1,
            "schema_version must be 1")
    require(nonempty(review.get("reviewer")), "reviewer is required")
    require(nonempty(review.get("reviewed_at")), "reviewed_at is required")
    reviewed = datetime.fromisoformat(review["reviewed_at"].replace("Z", "+00:00"))
    require(reviewed.utcoffset() is not None, "reviewed_at must include a timezone")
    require(isinstance(review.get("app_source_revision"), str) and
            re.fullmatch(r"[0-9a-f]{40}", review["app_source_revision"]) is not None,
            "app_source_revision must be a full Git revision")
    artifacts = review.get("artifacts")
    require(isinstance(artifacts, list) and len(artifacts) == 2,
            "artifacts must contain exactly the GIF and MP4")
    require(all(isinstance(a, dict) for a in artifacts), "artifacts must be objects")
    require(sorted(a.get("path", "") for a in artifacts) == sorted(ARTIFACTS),
            "both repository-relative artifact paths are required exactly once")
    for artifact in artifacts:
        path = artifact["path"]
        old, new = artifact.get("old_sha256"), artifact.get("new_sha256")
        require(digest(old) and digest(new), f"{path}: old/new SHA-256 must be lowercase hex")
        require(old != new, f"{path}: recording must replace the old artifact")
        content = (root / path).read_bytes()
        require(hashlib.sha256(content).hexdigest() == new, f"{path}: reviewed content hash mismatch")
        require(type(artifact.get("byte_size")) is int and artifact["byte_size"] == len(content) > 0,
                f"{path}: measured byte_size mismatch")
        require(nonempty(artifact.get("frame_rate")), f"{path}: frame_rate must be a rational string")
        rate, nominal, duration, frames = probe(root / path)
        require(Fraction(artifact["frame_rate"]) == rate, f"{path}: measured frame_rate mismatch")
        if path.endswith(".gif"):
            require(rate >= 10 and len(content) < 8_000_000, f"{path}: GIF must be >=10 fps and <8 MB")
        else:
            require(rate == nominal == 30, f"{path}: MP4 must be 30 fps")
        scenes = artifact.get("scenes")
        require(isinstance(scenes, list) and all(isinstance(s, dict) for s in scenes),
                f"{path}: scenes must be objects")
        require([s.get("id") for s in scenes] == list(SCENES), f"{path}: required scene IDs/order mismatch")
        previous_time, previous_index = -1, -1
        for scene in scenes:
            label = f"{path}: {scene['id']}"
            timestamp, index = scene.get("timestamp_seconds"), scene.get("frame_index")
            require(number(timestamp) and 0 <= timestamp and previous_time < timestamp < duration,
                    f"{label}: timestamp must increase and be within the encoded file")
            require(type(index) is int and previous_index < index < len(frames),
                    f"{label}: frame index must increase and be within the encoded file")
            end = frames[index + 1] if index + 1 < len(frames) else duration
            require(frames[index] - 0.00001 <= timestamp < end,
                    f"{label}: timestamp does not identify the recorded decoded frame")
            require(scene.get("result") == "pass", f"{label}: visual review must pass")
            require(nonempty(scene.get("observations")), f"{label}: concrete visual observations required")
            previous_time, previous_index = timestamp, index
    return len(artifacts)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", type=Path)
    args = parser.parse_args()
    try:
        count = validate(json.loads(args.evidence.read_text()))
    except (ValueError, TypeError, KeyError, OSError, ZeroDivisionError, subprocess.SubprocessError) as error:
        print(f"desktop review: {error}", file=sys.stderr)
        return 1
    print(f"desktop review: {count} encoded artifacts match complete visual review evidence")
    return 0


if __name__ == "__main__":
    sys.exit(main())
