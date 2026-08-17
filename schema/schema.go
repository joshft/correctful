// Package schema defines the correctful evidence model — the payload.
//
// correctful reads a change, derives the CLAIMS it makes, dispatches mechanical
// PROBES to test those claims, and emits a RECEIPT. The receipt's defining
// feature is the honest UNVERIFIED REMAINDER: the explicit accounting of every
// claim that nothing mechanically checked.
//
// The cardinal rule of the model: a claim's evidence TIER is a property of the
// PROBE that verified it, never an opinion offered by a language model. An LLM
// may PROPOSE claims; only a machine may raise a claim above T0.
package schema

// Tier is the strength of evidence a probe can confer on a claim.
//
// Tier is conferred by the probe, not asserted by a proposer. A probe declares
// the maximum tier it is capable of conferring (MaxTier); the actual tier a
// claim reaches is the highest MaxTier among the probes that ran AND passed
// against it.
type Tier int

const (
	// T0Unverified: no probe was bound, or a bound probe did not run. Every
	// claim at T0 belongs in the receipt's remainder.
	T0Unverified Tier = 0
	// T1Assertion: a single assertion passed (one test, one check).
	T1Assertion Tier = 1
	// T2Adversarial: an accept/reject pair passed — the positive case holds
	// AND the negative case is rejected.
	T2Adversarial Tier = 2
	// T3Property: a property-based, differential/parity, or model-checking
	// probe passed (property tests, cross-implementation parity, Alloy/TLA+).
	T3Property Tier = 3
	// T4Mechanical: a proof, an exhaustive mechanical check, or a runtime
	// observation passed (Dafny proof, exhaustive enumeration, eBPF-observed
	// behavior).
	T4Mechanical Tier = 4
)

// String renders a tier as its short label.
func (t Tier) String() string {
	switch t {
	case T0Unverified:
		return "T0-unverified"
	case T1Assertion:
		return "T1-assertion"
	case T2Adversarial:
		return "T2-adversarial"
	case T3Property:
		return "T3-property"
	case T4Mechanical:
		return "T4-mechanical"
	default:
		return "T?-invalid"
	}
}

// Shape is the taxonomy category of a claim. The set is open — new shapes are
// added as the corpus reveals them. These are the shapes Survey 01 surfaced.
type Shape string

const (
	// ShapeAssertion: a concrete assertion, typically named by a test.
	ShapeAssertion Shape = "assertion"
	// ShapeInvariant: a named invariant (INV/BND/PRH/ABS-style identifiers).
	ShapeInvariant Shape = "invariant"
	// ShapeCoupledFields: two or more fields/structures that must move in
	// lockstep (the PMB-013 recurrence — first-class per Survey 01).
	ShapeCoupledFields Shape = "coupled-fields-lockstep"
	// ShapeMustClause: a normative MUST/MUST-NOT clause from an RFC or spec.
	// Usually reaches only T0 until a probe is bound — a common remainder tenant.
	ShapeMustClause Shape = "must-clause"
	// ShapeSafetyAssert: a formal safety assertion (e.g. an Alloy `assert`).
	ShapeSafetyAssert Shape = "safety-assert"
	// ShapeWitness: the model must ADMIT a scenario (e.g. an Alloy `run`
	// command finding an instance). The vacuity guard: a fleet of passing
	// safety checks means nothing if the model is inconsistent and admits no
	// traces at all. Pass semantics are inverted relative to a check — a
	// witness passes when a solution EXISTS.
	ShapeWitness Shape = "witness"
)

// SourceKind names where a claim was harvested from.
type SourceKind string

const (
	SourceGoTest     SourceKind = "go-test"     // a Go test function name
	SourceDotnetTest SourceKind = "dotnet-test" // a .NET [Fact]/[Theory] test method
	SourceSpecID     SourceKind = "spec-id"     // an INV/BND/PRH/ABS identifier in source or spec
	SourceAlloyAssert SourceKind = "alloy-assert" // an Alloy `assert` block
	SourceAlloyRun   SourceKind = "alloy-run"   // an Alloy `run` command (witness)
	SourceRFCMust    SourceKind = "rfc-must"    // a MUST/MUST-NOT clause in an RFC
)

// Source is the provenance of a harvested claim — the evidence trail back to the
// artifact the claim was read from.
type Source struct {
	Kind SourceKind `json:"kind"`
	File string     `json:"file"`
	Line int        `json:"line,omitempty"`
	Ref  string     `json:"ref"` // the raw token, e.g. the test function name
}

// Claim is something a change asserts to be true.
//
// A claim is PROPOSED (by a harvester, a heuristic, or — later — an LLM) and
// then VERIFIED (by a probe) or not. A claim with an empty ProbeID that nothing
// ran against ends in the remainder. That is not a failure of correctful; it is
// correctful being honest about the boundary of what was checked.
type Claim struct {
	ID     string `json:"id"`     // stable identifier, e.g. "INV-009" or a derived slug
	Shape  Shape  `json:"shape"`  // taxonomy category
	Text   string `json:"text"`   // human-readable statement
	Source Source `json:"source"` // where it was harvested from
	// ProbeIDs names the probes bound to test this claim. Empty means no probe
	// is bound — the claim is destined for the remainder unless one is later
	// bound. A claim may carry several probes (several tests for one invariant,
	// or a compound accept/reject pair). Every bound probe runs, and a
	// refutation by ANY of them refutes the claim — passing probes never
	// outvote a failing one.
	ProbeIDs []string `json:"probe_ids,omitempty"`
}

// Evidence is the outcome of running one probe against one claim.
type Evidence struct {
	ClaimID  string `json:"claim_id"`
	ProbeID  string `json:"probe_id"`
	Tier     Tier   `json:"tier"`   // the tier this probe confers when it passes
	Ran      bool   `json:"ran"`    // did the probe actually execute?
	Passed   bool   `json:"passed"` // did it pass? (meaningful only when Ran)
	Detail   string `json:"detail,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// Verified reports whether this evidence raises its claim: the probe ran and
// passed. A probe that ran and FAILED refutes the claim; a probe that did not
// run says nothing.
func (e Evidence) Verified() bool { return e.Ran && e.Passed }

// Refuted reports whether this evidence refutes its claim: the probe ran and
// the claim did not hold.
func (e Evidence) Refuted() bool { return e.Ran && !e.Passed }

// Status is a claim's standing after all its evidence is weighed.
type Status string

const (
	// StatusVerified: at least one probe ran and passed. EffectiveTier > T0.
	StatusVerified Status = "verified"
	// StatusRefuted: at least one probe ran and failed, and none passed at a
	// higher tier. The merge gate should block on refuted claims.
	StatusRefuted Status = "refuted"
	// StatusUnverified: nothing ran that could raise the claim. Remainder.
	StatusUnverified Status = "unverified"
)

// ChangeRef identifies the change a receipt is about.
type ChangeRef struct {
	Repo     string   `json:"repo,omitempty"`
	BaseRef  string   `json:"base_ref"`
	HeadRef  string   `json:"head_ref"`
	Files    []string `json:"files"`
}

// ClaimResult is a claim joined with its weighed standing — the row a reader
// scans in a receipt.
type ClaimResult struct {
	Claim         Claim      `json:"claim"`
	Status        Status     `json:"status"`
	EffectiveTier Tier       `json:"effective_tier"`
	Evidence      []Evidence `json:"evidence,omitempty"`
}

// Summary is the headline arithmetic of a receipt.
type Summary struct {
	TotalClaims int            `json:"total_claims"`
	Verified    int            `json:"verified"`
	Refuted     int            `json:"refuted"`
	Unverified  int            `json:"unverified"` // == len(Remainder)
	// TierCounts is keyed by tier label (e.g. "T1-assertion") for a readable
	// payload.
	TierCounts map[string]int `json:"tier_counts"`
}

// FileCoverage records what the harvest actually did with one changed file.
type FileCoverage struct {
	File string `json:"file"`
	// ReadBy names the harvesters that opened and understood this file. The
	// names carry depth information: a file read ONLY by "spec-ref" was
	// identifier-scanned, not parsed for its native claim constructs.
	ReadBy []string `json:"read_by,omitempty"`
	// Claims counts claims sourced from this file BEFORE reconciliation, so a
	// file whose claims merged into another file's claim still counts as
	// contributing.
	Claims int `json:"claims"`
}

// Coverage is the receipt's disclosure of its own blind spots: which changed
// files produced claims, which were read but yielded none, and — the honest
// part — which files NO harvester could read at all. A receipt that verifies
// 17 of 17 claims while 397 of 414 files went unread must say so; absence is
// stated, never implied. This is the remainder philosophy applied to the tool
// itself.
type Coverage struct {
	Files   []FileCoverage `json:"files"`
	Claimed int            `json:"claimed"` // files sourcing ≥1 claim
	Scanned int            `json:"scanned"` // read by ≥1 harvester, 0 claims
	Unread  int            `json:"unread"`  // no harvester read the file
}

// Receipt is the per-change output: what was claimed, what was verified, and —
// the load-bearing part — the honest unverified remainder.
type Receipt struct {
	SchemaVersion string        `json:"schema_version"`
	Change        ChangeRef     `json:"change"`
	Results       []ClaimResult `json:"results"`
	// Remainder is the subset of Results with Status == StatusUnverified,
	// surfaced explicitly so a reader never has to derive it. This is the
	// feature no other tool in the field ships.
	Remainder []ClaimResult `json:"remainder"`
	Coverage  Coverage      `json:"coverage"`
	Summary   Summary       `json:"summary"`
}

// SchemaVersion is the current version of the receipt schema (the payload).
const SchemaVersion = "0.0.2"
