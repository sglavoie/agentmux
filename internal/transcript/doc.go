// Package transcript discovers, parses, and searches harness session
// transcripts (Claude, Copilot, Codex). Each harness writes its own JSONL
// layout under a per-profile home directory; per-harness Reader
// implementations expose a uniform view of those files to the rest of amx.
//
// The transcript files are the authoritative source and are never rewritten
// here. transcripts.json and its search sidecars are a disposable metadata
// cache over them: refresh, pruning, rebuild, and corruption quarantine may
// replace the cache, always through advisory lock sidecars and atomic renames,
// while the source files stay untouched. Search is source-backed, falling back
// to the transcripts themselves when a sidecar is missing.
//
// # The metadata admission path
//
// Candidate, Authorizer, MetadataDiscoverer, and History are a second and
// narrower walk of the same corpus. They read conversation identity through
// per-harness structs that have no field for message content, authorize each
// conversation against the single working directory it names, and only then
// open it.
//
// The ordinary discovery path cannot serve as that boundary. Reader.Discover
// streams every message body to build the snippet, the count, and the search
// text, and Index.Refresh writes that text into a sidecar, so by the time a
// refresh has decided anything about a transcript it has already read all of
// it. History never touches Index, LoadIndex, or a search sidecar, and its
// signature is the proof.
//
// Zero named directories and more than one are both refusals, because an
// unidentifiable conversation cannot be shown to be outside an exclusion and a
// mixed-project one could only be sorted out by reading the content the
// decision gates. admission.go carries the full rationale.
package transcript
