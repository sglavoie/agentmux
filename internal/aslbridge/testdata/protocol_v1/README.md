# Bridge protocol 1 wire fixtures

Byte-identical copy of `tests/fixtures/asl_bridge_protocol_v1/` in the
agent-session-loop repository, which owns the protocol and is the only place
these frames are edited. `manifest.json` pins the vocabulary and a SHA-256 per
frame file; `protocol_fixtures_test.go` recomputes the combined digest, so a
copy that drifts from the source fails here.

To refresh: change the frames in agent-session-loop, run its test suite, then
copy every `*.json` file across unchanged and run `just asl-integration`.
