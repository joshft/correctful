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
	SourceGoTest      SourceKind = "go-test"      // a Go test function name
	SourceDotnetTest  SourceKind = "dotnet-test"  // a .NET [Fact]/[Theory] test method
	SourceSpecID      SourceKind = "spec-id"      // an INV/BND/PRH/ABS identifier in source or spec
	SourceAlloyAssert SourceKind = "alloy-assert" // an Alloy `assert` block
	SourceAlloyRun    SourceKind = "alloy-run"    // an Alloy `run` command (witness)
	SourceRFCMust     SourceKind = "rfc-must"     // a MUST/MUST-NOT clause in an RFC
	SourceSpecDef     SourceKind = "spec-def"     // a definition heading in the spec corpus
	// SourceLLM marks a claim PROPOSED by a language model reading the diff.
	// Such claims are unverified by construction: an LLM may propose, only a
	// machine may raise a claim above T0 (the cardinal rule). They exist so
	// the remainder can be honest on changes that never wrote their claims
	// down — the receipt states what the change implicitly asserts and that
	// nothing checked it.
	SourceLLM SourceKind = "llm-proposed"
)

// Source is the provenance of a harvested claim — the evidence trail back to the
// artifact the claim was read from.
type Source struct {
	Kind SourceKind `json:"kind"`
	File string     `json:"file"`
	Line int        `json:"line,omitempty"`
	Ref  string     `json:"ref"` // the raw token, e.g. the test function name
}

// AnchorStatus is the outcome of resolving a claim's identifier against the
// repo's definition corpus — the first rung of proof-carrying binding. A name
// like INV-004 in a test is an ATTESTATION; anchoring asks whether the id even
// exists as a stated invariant, and refuses to pretend when it cannot tell.
type AnchorStatus string

const (
	// AnchorResolved: the id has exactly one definition — one distinct title,
	// possibly repeated verbatim in several documents (a spec quoted by a
	// review artifact still names one invariant). The claim's Text is
	// upgraded to the definition's own words.
	AnchorResolved AnchorStatus = "resolved"
	// AnchorAmbiguous: the id is defined in several places with DIFFERENT
	// titles. Id namespaces are feature-local in practice — a spec corpus
	// commonly restarts INV-001 per feature — so a bare id cannot say which
	// invariant it means. correctful discloses the collision; it never picks.
	AnchorAmbiguous AnchorStatus = "ambiguous"
	// AnchorOrphan: the corpus defines identifiers, but not this one. The
	// claim rests on a name attestation with no definition behind it — a
	// stale, mistyped, or invented id.
	AnchorOrphan AnchorStatus = "orphan"
)

// Anchor records how a spec-id claim resolved against the definition corpus.
// Claims whose id is not a spec identifier carry no Anchor; likewise every
// claim in a repo with no definition corpus at all — a repo that never states
// invariants in documents should not have every claim flagged for it.
type Anchor struct {
	Status AnchorStatus `json:"status"`
	// Title is the definition's title, set only when resolved.
	Title string `json:"title,omitempty"`
	// Sites are the definition heading locations: all agreeing sites when
	// resolved, every colliding site when ambiguous, empty when orphan.
	Sites []Source `json:"sites,omitempty"`
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
	// Anchor is the claim id's resolution against the repo's definition
	// corpus (spec-id claims only; nil when no corpus exists).
	Anchor *Anchor `json:"anchor,omitempty"`
	// RefSites are the places SHIPPED CODE names this claim's id — the
	// annotated regions a coverage-proven binding checks a probe against.
	// Populated by the reference harvester and unioned through claim merges.
	RefSites []Source `json:"ref_sites,omitempty"`
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
	// Binding states how strongly this probe is tied to THIS claim — the
	// second rung of proof-carrying binding, orthogonal to the tier. See the
	// Binding* constants for the vocabulary. Empty: no coverage check applied
	// (no code sites, or the probe kind has no prover yet).
	Binding string `json:"binding,omitempty"`
	// Mechanism identifies the probe kind that produced this evidence — the
	// class axis a policy floor needs beside the tier ("this path requires a
	// T2 adversarial pair"). Set by the runner from its own identity, so a
	// policy engine never parses probe ids. Empty only when no runner ran.
	Mechanism string `json:"mechanism,omitempty"`
	// Scope is the probe's MEASURED execution footprint, known only for
	// instrumented runs: ScopeSinglePackage when every executed block falls
	// in one package directory, ScopeCrossPackage when they span more.
	// Empty means unmeasured (no coverage profile) — never assumed.
	Scope string `json:"scope,omitempty"`
	// Environment records the toolchain the probe ran under, when the runner
	// measures it (e.g. "go1.24.5 linux/amd64"). Empty means unmeasured.
	Environment string `json:"environment,omitempty"`
}

// Mechanism values — one per runner kind.
const (
	MechanismGoTest     = "go-test"
	MechanismGoTestPair = "go-test-pair"
	MechanismDotnetTest = "dotnet-test"
	MechanismAlloyCheck = "alloy-check"
)

// Scope values — the measured execution footprint of an instrumented run.
const (
	ScopeSinglePackage = "single-package"
	ScopeCrossPackage  = "cross-package"
)

// Binding values — how a probe→claim edge was checked. The first two apply to
// claims whose id is annotated in shipped code (RefSites); the file-level pair
// applies to LLM-proposed edges, where the claim carries a file but no line.
const (
	// BindingCovered: the probe's execution demonstrably reached the
	// enclosing function of a code site naming the claim's id — proof the
	// test exercises the annotated region (still not proof it asserts the
	// right property).
	BindingCovered = "covered"
	// BindingNameOnly: the binding was checked and NO annotated region was
	// reached — the tie between this probe and this claim is the name alone.
	BindingNameOnly = "name-only"
	// BindingFileCovered: the probe's execution demonstrably reached the file
	// the claim is about. The file-level analogue of BindingCovered, used for
	// model-proposed edges: it is what makes such an edge count at all.
	BindingFileCovered = "file-covered"
	// BindingFileNotReached: the probe ran instrumented and its execution
	// never reached the claim's file — the proposed edge is refuted as an
	// edge (which says nothing about the claim itself).
	BindingFileNotReached = "file-not-reached"
)

// Verified reports whether this evidence raises its claim: the probe ran,
// passed, AND confers a tier above T0. A pass at T0 confers nothing — T0 IS
// the unverified tier, so counting such a pass as verification would mint
// StatusVerified with an effective tier that means "nothing checked this".
// A probe that ran and FAILED refutes the claim; a probe that did not run
// says nothing.
func (e Evidence) Verified() bool { return e.Ran && e.Passed && e.Tier > T0Unverified }

// Refuted reports whether this evidence refutes its claim: the probe ran and
// the claim did not hold.
func (e Evidence) Refuted() bool { return e.Ran && !e.Passed }

// Status is a claim's standing after all its evidence is weighed.
type Status string

const (
	// StatusVerified: at least one probe ran and passed. EffectiveTier > T0.
	StatusVerified Status = "verified"
	// StatusRefuted: at least one probe ran and failed. Refutation dominates
	// unconditionally — no volume or tier of passing probes outweighs a
	// failure. The merge gate should block on refuted claims.
	StatusRefuted Status = "refuted"
	// StatusUnverified: nothing ran that could raise the claim. Remainder.
	StatusUnverified Status = "unverified"
)

// ChangeRef identifies the change a receipt is about. Repo is the repository
// NAME only — a receipt is shareable, and a local filesystem location is not
// its reader's business. BaseSHA/HeadSHA pin the receipt to immutable inputs
// (BaseSHA = the merge base actually diffed against); the symbolic refs are
// kept for readability but move over time.
type ChangeRef struct {
	Repo    string   `json:"repo,omitempty"`
	BaseRef string   `json:"base_ref"`
	HeadRef string   `json:"head_ref"`
	BaseSHA string   `json:"base_sha,omitempty"`
	HeadSHA string   `json:"head_sha,omitempty"`
	Files   []string `json:"files"`
	// Excluded discloses files the change resolver DELIBERATELY left out of
	// Files. They never reach the harvest, so the coverage section cannot
	// account for them — the scope boundary must state its own blind spot.
	Excluded []Exclusion `json:"excluded,omitempty"`
	// InputDigest is a SHA-256 pin over the exact content harvested (sorted
	// path + per-file content hash; see gitdiff.InputDigest for the formula).
	// HeadSHA identifies a clean tree; a mid-branch receipt over staged,
	// unstaged, or untracked work is reproducible only through this.
	InputDigest string `json:"input_digest,omitempty"`
}

// Exclusion is one scope rule's deliberate effect on the change: how many
// files it left out, why, and (for the territory rule) which top-level trees
// they lived in. Counts, not paths — the measured case was thousands of
// cache files, and listing them would drown the receipt in exactly the noise
// the rule exists to exclude.
type Exclusion struct {
	Reason string   `json:"reason"`         // "untracked-territory" | "untracked-hidden"
	Count  int      `json:"count"`          // files excluded by this rule
	Dirs   []string `json:"dirs,omitempty"` // top-level trees affected, sorted (territory only)
}

// ClaimResult is a claim joined with its weighed standing — the row a reader
// scans in a receipt.
type ClaimResult struct {
	Claim         Claim      `json:"claim"`
	Status        Status     `json:"status"`
	EffectiveTier Tier       `json:"effective_tier"`
	Evidence      []Evidence `json:"evidence,omitempty"`
}

// AnchoringSummary is the headline arithmetic of the binding layer: of the
// claims whose id is a spec identifier, how many rest on a real definition.
type AnchoringSummary struct {
	SpecIDClaims int `json:"spec_id_claims"`
	Resolved     int `json:"resolved"`
	Ambiguous    int `json:"ambiguous"`
	Orphan       int `json:"orphan"`
}

// Summary is the headline arithmetic of a receipt.
type Summary struct {
	TotalClaims int `json:"total_claims"`
	Verified    int `json:"verified"`
	Refuted     int `json:"refuted"`
	Unverified  int `json:"unverified"` // == len(Remainder)
	// TierCounts is keyed by tier label (e.g. "T1-assertion") for a readable
	// payload.
	TierCounts map[string]int `json:"tier_counts"`
	// Anchoring is present when the repo has a definition corpus.
	Anchoring *AnchoringSummary `json:"anchoring,omitempty"`
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
	// SkipReason states WHY an unread file was not read — the two causes are
	// different disclosures: "hidden-path" means policy skipped it (the file
	// lives under a hidden directory, which every harvester treats as
	// installed tooling), while "no-harvester" means a capability gap (no
	// harvester understands the format). Empty for read files.
	SkipReason string `json:"skip_reason,omitempty"`
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
	Unread  int            `json:"unread"`  // no harvester read the file (total, both causes)
	// UnreadPolicy counts the subset of Unread that policy skipped
	// (SkipReason "hidden-path") rather than a capability gap. The receipt
	// renders the two causes as separate disclosures.
	UnreadPolicy int `json:"unread_policy,omitempty"`
	// SuppressedMentions counts spec-id sightings that were NOT minted as
	// claims because the repo defines no spec-id corpus at all: with no
	// definition anywhere, a reference has no possible referent — it is a
	// MENTION of an identifier, not a claim about a defined invariant. The
	// suppression is disclosed here because dropping entries from the
	// remainder silently would be exactly the dishonesty the remainder
	// exists to prevent.
	SuppressedMentions int `json:"suppressed_mentions,omitempty"`
}

// Receipt is the per-change output: what was claimed, what was verified, and —
// the load-bearing part — the honest unverified remainder.
type Receipt struct {
	SchemaVersion string `json:"schema_version"`
	// ToolVersion identifies the checker build that produced this receipt —
	// module version plus VCS revision when the build carries them, so two
	// receipts are comparable across time only when the harvesters and
	// runners that produced them are known. One field covers every in-tree
	// component (they ship in one binary); per-supplier versions arrive with
	// the evidence-intake contract. "unknown" when the build carries no
	// identity — stated, never guessed.
	ToolVersion string        `json:"tool_version,omitempty"`
	Change      ChangeRef     `json:"change"`
	Results     []ClaimResult `json:"results"`
	// Remainder is the subset of Results with Status == StatusUnverified,
	// surfaced explicitly so a reader never has to derive it. This is the
	// feature no other tool in the field ships.
	Remainder []ClaimResult `json:"remainder"`
	Coverage  Coverage      `json:"coverage"`
	Summary   Summary       `json:"summary"`
}

// SchemaVersion is the current version of the receipt schema (the payload).
const SchemaVersion = "0.0.10"
