// The evidence record's shape: the release artifact that outlives the terminal
// the gate ran in, and every sub-record a consumer reads out of it.

package release

// Step is one planned command. The plan is data rather than control flow so a
// test can assert the exact ordered command list without executing any of it,
// and so the evidence record can list steps that never ran.
type Step struct {
	Name  string   `json:"name"`
	Repo  string   `json:"repo"`
	Phase string   `json:"phase"`
	Argv  []string `json:"command"`
}

// StepResult is a Step plus what happened to it.
type StepResult struct {
	Name       string   `json:"name"`
	Repo       string   `json:"repo"`
	Phase      string   `json:"phase"`
	Command    []string `json:"command"`
	Status     string   `json:"status"`
	ExitCode   int      `json:"exit_code"`
	DurationMS int64    `json:"duration_ms"`
	// OutputTail is recorded only for a failed step. A passing gate's evidence
	// would otherwise carry megabytes of test output that no consumer reads and
	// that makes two identical runs differ byte for byte; a failing one needs
	// the tail to be diagnosable at all.
	OutputTail string `json:"output_tail,omitempty"`
}

// RepoEvidence records what a source checkout was at gate time.
type RepoEvidence struct {
	Repo     string `json:"repo"`
	Root     string `json:"root"`
	Revision string `json:"revision"`
	Describe string `json:"describe"`
	Clean    bool   `json:"clean"`
	// Dirty lists the porcelain status lines when Clean is false. A release
	// built from an unrecorded working tree is unreproducible, so the gate
	// refuses -- and says exactly which paths made it refuse.
	Dirty []string `json:"dirty,omitempty"`
}

// Artifact is a hashed release artifact. Kind distinguishes a single file's
// digest from a source-tree digest, because they are computed differently and a
// consumer must not compare one against the other.
type Artifact struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Kind   string `json:"kind"` // "file" or "tree"
	SHA256 string `json:"sha256"`
}

// ContractEvidence pins the compatibility contract the pair agrees on.
type ContractEvidence struct {
	SetID          string `json:"set_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// HarnessVersion is one row of `asl harness-compat --json`, reduced to the
// fields a release record needs.
type HarnessVersion struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// Environment records the machine the pair was actually exercised on.
type Environment struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	OSVersion string `json:"os_version"`
	Go        string `json:"go"`
	// Kitty is the Kitty version a schema-1 record named. The gate stopped
	// asking in schema 2, when Kitty became an optional backend it no longer
	// exercises: a version there would read as a terminal the release was
	// tested in.
	Kitty     string           `json:"kitty,omitempty"`
	Harnesses []HarnessVersion `json:"harnesses"`
}

// Signature describes the detached signature beside the record. The signature
// covers the evidence file's bytes, so it cannot live inside them.
type Signature struct {
	Signed    bool   `json:"signed"`
	Namespace string `json:"namespace"`
	Path      string `json:"path,omitempty"`
	KeyPath   string `json:"key_path,omitempty"`
}

// TagEvidence records an annotated tag the gate wrote.
type TagEvidence struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

// TagFailure is the account of a pair-tagging attempt that did not complete.
//
// Tagging writes two refs in two repositories and is therefore the only part of
// the gate that can be half-done. Both outcomes are recorded because they call
// for different actions: RolledBack means the pair is untagged and the attempt
// can simply be repeated, while Stranded means a ref survives that will block
// the next attempt until somebody removes it.
type TagFailure struct {
	// Error is why the attempt stopped.
	Error string `json:"error"`
	// RolledBack lists tags this run created and then removed. They are absent
	// from Evidence.Tags because they no longer exist.
	RolledBack []TagEvidence `json:"rolled_back,omitempty"`
	// Stranded lists tags this run created and could not remove. They are absent
	// from Evidence.Tags too: that field means "this pair is tagged", and a pair
	// tagged on one side is not.
	Stranded []TagEvidence `json:"stranded,omitempty"`
	// RollbackError is why the removal failed, when one did.
	RollbackError string `json:"rollback_error,omitempty"`
	// Recovery is a command per stranded tag whose object this run identified,
	// in the same order those tags appear in Stranded. It names the object as
	// well as the ref, so running it late cannot delete a tag somebody else has
	// since put under that name -- which is the only reason it is safe to
	// publish an executable deletion at all.
	//
	// A stranded tag this run could not identify has no entry here. It is listed
	// in Unidentified instead, because the gate has no command it can honestly
	// tell an operator to run against it.
	Recovery []string `json:"recovery,omitempty"`
	// Unidentified lists stranded tags whose object this run never established.
	// They also appear in Stranded; this is the account of why no deletion is
	// offered for them.
	Unidentified []UnidentifiedTag `json:"unidentified,omitempty"`
}

// UnidentifiedTag is a ref this run created, could not identify, and therefore
// could not offer to remove.
//
// It exists because the alternative is worse than saying nothing. The gate reads
// the object back immediately after writing the tag, and when that read fails the
// ref is real but nameless: the run knows a tag exists under this name and does
// not know that the object under it is the one it wrote. Every safe removal the
// gate performs is a compare-and-delete against that object, so without it there
// is no safe removal to publish -- and publishing `git tag -d <name>` instead
// would hand an operator a command that deletes whatever now answers to the name,
// which is precisely the outcome the compare-and-delete exists to refuse.
//
// So this carries the non-destructive inspection that establishes what is
// actually there, plus the judgement the gate cannot make on the operator's
// behalf. Nothing in it is executable as a deletion, and a consumer that runs
// every command the record publishes deletes nothing it should not.
type UnidentifiedTag struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
	// Reason is why this run has no object to compare against.
	Reason string `json:"reason"`
	// Inspect are commands that report what the ref points at now and what that
	// object says about itself. They are read-only: running them, twice or a
	// year later, changes nothing.
	Inspect []string `json:"inspect"`
	// Requirement is what an operator must establish before deleting anything,
	// and the shape of the deletion once they have. It names an object the
	// inspection supplies rather than a placeholder the gate could fill in,
	// because the gate filling it in is exactly what it cannot do.
	Requirement string `json:"requirement"`
}

// Evidence is the complete release record.
type Evidence struct {
	SchemaVersion int              `json:"schema_version"`
	PairID        string           `json:"pair_id"`
	GeneratedAt   string           `json:"generated_at"`
	Result        string           `json:"result"`
	Repositories  []RepoEvidence   `json:"repositories"`
	Artifacts     []Artifact       `json:"artifacts"`
	Contract      ContractEvidence `json:"contract"`
	Environment   Environment      `json:"environment"`
	Steps         []StepResult     `json:"steps"`
	Signature     Signature        `json:"signature"`
	Tags          []TagEvidence    `json:"tags,omitempty"`
	// TagFailure describes a tagging attempt that did not complete. It is
	// present only when one did not, and it is never populated at the same time
	// as Tags.
	TagFailure *TagFailure `json:"tag_failure,omitempty"`
	// Path is where the record was written. It is not part of the signed
	// content's meaning, but it is what an operator needs to verify it.
	Path string `json:"path,omitempty"`
}

// Passed reports whether the pair was released: every planned step ran and
// passed, and nothing afterwards left the pair in a state the record has to
// qualify. StatusPartial, StatusNotReleased and StatusTagging are deliberately
// not passed.
//
// The second condition is checked rather than trusted. Every path that
// qualifies a tagging attempt already downgrades the result, so the two can only
// disagree if a later one forgets to -- and the cost of that omission is a
// record whose own account of an incomplete tagging attempt is overruled by the
// single field most consumers read. A tag_failure is the record qualifying its
// outcome; there is no reading under which such a record describes a released
// pair.
func (e *Evidence) Passed() bool { return e.Result == StatusPassed && e.TagFailure == nil }
