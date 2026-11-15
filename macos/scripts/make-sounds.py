# Generate Agent Mux's needs-you.wav and finished.wav sounds.
# Run: python3 macos/scripts/make-sounds.py [--check]
# The two WAV files may be replaced by hand-made ones of the same names.
# In that case --check is no longer meaningful; the person replacing them
# should remove it from the plan's checks.

import math
from pathlib import Path
import struct
import sys


SAMPLE_RATE = 44_100
SOUNDS = {
    "needs-you.wav": ((660, 0.14), (880, 0.14)),
    "finished.wav": ((523.25, 0.20),),
}
OUTPUT = Path(__file__).resolve().parents[1] / "AgentMux/Resources/Sounds"


def make_sound(notes):
    samples = []
    for frequency, duration in notes:
        count = round(duration * SAMPLE_RATE)
        for index in range(count):
            t = index / SAMPLE_RATE
            phase = 2 * math.pi * frequency * t
            tone = math.sin(phase) + 0.3 * math.sin(2 * phase)
            envelope = min(t / 0.005, 1) * math.exp(-5 * t / duration)
            # End each note at silence, including the join between two notes.
            samples.append(tone * envelope if index < count - 1 else 0)
    scale = 0.7 * 32767 / max(abs(sample) for sample in samples)
    pcm = b"".join(struct.pack("<h", round(sample * scale)) for sample in samples)
    # Canonical PCM WAV header: mono, 44.1 kHz, signed 16-bit samples.
    header = struct.pack(
        "<4sI4s4sIHHIIHH4sI",
        b"RIFF", 36 + len(pcm), b"WAVE", b"fmt ", 16,
        1, 1, SAMPLE_RATE, SAMPLE_RATE * 2, 2, 16, b"data", len(pcm),
    )
    return header + pcm


def main():
    if sys.argv[1:] not in ([], ["--check"]):
        print("usage: make-sounds.py [--check]", file=sys.stderr)
        return 2
    check = sys.argv[1:] == ["--check"]
    if not check:
        OUTPUT.mkdir(parents=True, exist_ok=True)
    failed = False
    for name, notes in SOUNDS.items():
        expected = make_sound(notes)
        path = OUTPUT / name
        if check:
            if not path.is_file() or path.read_bytes() != expected:
                print(f"{path}: missing or differs from generated sound", file=sys.stderr)
                failed = True
        else:
            path.write_bytes(expected)
            print(path)
    return int(failed)


if __name__ == "__main__":
    sys.exit(main())
