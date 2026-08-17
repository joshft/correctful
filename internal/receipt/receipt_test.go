package receipt

import (
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/schema"
)

func sampleClaims() ([]schema.Claim, [][]schema.Evidence) {
	claims := []schema.Claim{
		{ID: "A1", Shape: schema.ShapeAssertion, Text: "passes", ProbeIDs: []string{"go-test:x:TestA"}},
		{ID: "A2", Shape: schema.ShapeAssertion, Text: "fails", ProbeIDs: []string{"go-test:x:TestB"}},
		{ID: "A3", Shape: schema.ShapeInvariant, Text: "unchecked",
			Source: schema.Source{File: "y.go", Line: 7}},
	}
	evidence := [][]schema.Evidence{
		{{ClaimID: "A1", ProbeID: "go-test:x:TestA", Tier: schema.T1Assertion, Ran: true, Passed: true}},
		{{ClaimID: "A2", ProbeID: "go-test:x:TestB", Tier: schema.T1Assertion, Ran: true, Passed: false}},
		{{ClaimID: "A3", Ran: false, Detail: "no probe bound"}},
	}
	return claims, evidence
}

// TestINV001_RemainderContainsEveryUnverifiedClaim: every claim that nothing
// verified must appear in the remainder — the receipt's load-bearing guarantee.
func TestINV001_RemainderContainsEveryUnverifiedClaim(t *testing.T) {
	claims, evidence := sampleClaims()
	r := Assemble(gitdiff.Change{BaseRef: "main", HeadRef: "wip"}, claims, evidence, schema.Coverage{})

	if got := len(r.Remainder); got != 1 {
		t.Fatalf("remainder size = %d, want 1", got)
	}
	if r.Remainder[0].Claim.ID != "A3" {
		t.Fatalf("remainder[0] = %q, want A3", r.Remainder[0].Claim.ID)
	}
	if r.Summary.Unverified != len(r.Remainder) {
		t.Fatalf("summary.Unverified=%d != len(remainder)=%d", r.Summary.Unverified, len(r.Remainder))
	}
}

// TestINV002_RefutedClaimNeverEntersRemainder: a claim a probe actively refuted
// is a failure, not an unknown — it must never be laundered into the remainder.
func TestINV002_RefutedClaimNeverEntersRemainder(t *testing.T) {
	claims, evidence := sampleClaims()
	r := Assemble(gitdiff.Change{}, claims, evidence, schema.Coverage{})

	for _, res := range r.Remainder {
		if res.Claim.ID == "A2" {
			t.Fatalf("refuted claim A2 leaked into the remainder")
		}
	}
	if r.Summary.Refuted != 1 {
		t.Fatalf("summary.Refuted = %d, want 1", r.Summary.Refuted)
	}
}

// TestVerifiedClaimCarriesProbeTier: a verified claim's effective tier is the
// tier its probe conferred — not an opinion, the probe's fixed strength.
func TestVerifiedClaimCarriesProbeTier(t *testing.T) {
	claims, evidence := sampleClaims()
	r := Assemble(gitdiff.Change{}, claims, evidence, schema.Coverage{})

	var a1 schema.ClaimResult
	for _, res := range r.Results {
		if res.Claim.ID == "A1" {
			a1 = res
		}
	}
	if a1.Status != schema.StatusVerified {
		t.Fatalf("A1 status = %q, want verified", a1.Status)
	}
	if a1.EffectiveTier != schema.T1Assertion {
		t.Fatalf("A1 tier = %v, want T1", a1.EffectiveTier)
	}
}

// TestINV007_RefutationDominatesVerification: a claim with several probes where
// even one refutes does NOT hold — passing probes never outvote a failing one.
// Without this rule, a multi-test invariant with four passes and one failure
// would render as verified and the gate would wave a defect through.
func TestINV007_RefutationDominatesVerification(t *testing.T) {
	claims := []schema.Claim{{ID: "M1", Shape: schema.ShapeInvariant,
		ProbeIDs: []string{"go-test:x:TestA", "go-test:x:TestB"}}}
	evidence := [][]schema.Evidence{{
		{ClaimID: "M1", ProbeID: "go-test:x:TestA", Tier: schema.T2Adversarial, Ran: true, Passed: true},
		{ClaimID: "M1", ProbeID: "go-test:x:TestB", Tier: schema.T1Assertion, Ran: true, Passed: false},
	}}
	r := Assemble(gitdiff.Change{}, claims, evidence, schema.Coverage{})
	if r.Results[0].Status != schema.StatusRefuted {
		t.Fatalf("status = %q, want refuted (a passing probe outvoted a failing one)", r.Results[0].Status)
	}
	if r.Summary.Refuted != 1 || r.Summary.Verified != 0 {
		t.Fatalf("summary refuted=%d verified=%d, want 1/0", r.Summary.Refuted, r.Summary.Verified)
	}
}

// TestRemainderSectionAlwaysRenders: the text receipt states the remainder even
// when it is empty, so its absence is a declared result, not an omission.
func TestRemainderSectionAlwaysRenders(t *testing.T) {
	r := Assemble(gitdiff.Change{}, nil, nil, schema.Coverage{})
	var b strings.Builder
	WriteText(&b, r)
	if !strings.Contains(b.String(), "UNVERIFIED REMAINDER") {
		t.Fatalf("text receipt omitted the remainder section")
	}
	if !strings.Contains(b.String(), "HARVEST COVERAGE") {
		t.Fatalf("text receipt omitted the coverage disclosure")
	}
}

// TestCoverageDisclosesUnreadFiles: a receipt whose change includes files no
// harvester read must say so — a "17/17 verified" headline over a mostly-unread
// change is the lie of omission this section exists to prevent.
func TestCoverageDisclosesUnreadFiles(t *testing.T) {
	cov := schema.Coverage{
		Files: []schema.FileCoverage{
			{File: "formal/model.als", ReadBy: []string{"alloy"}, Claims: 17},
			{File: "src/core.c"},
			{File: "src/other.c"},
			{File: "docs/spec.md"},
		},
		Claimed: 1, Unread: 3,
	}
	r := Assemble(gitdiff.Change{}, nil, nil, cov)
	var b strings.Builder
	WriteText(&b, r)
	out := b.String()
	if !strings.Contains(out, "3 unread") {
		t.Errorf("unread count not disclosed:\n%s", out)
	}
	if !strings.Contains(out, ".c×2") || !strings.Contains(out, ".md×1") {
		t.Errorf("unread extension histogram missing:\n%s", out)
	}
}

// TestAnchoringSummaryAndMarkers: the receipt discloses the binding layer —
// headline counts plus per-row markers for the two distrust states (orphan,
// ambiguous). Resolved claims carry no marker; their upgraded text IS the
// disclosure. A claim set with no anchors yields no anchoring line at all.
func TestAnchoringSummaryAndMarkers(t *testing.T) {
	claims := []schema.Claim{
		{ID: "INV-001", Text: "INV-001: loader accepts only signed bundles",
			Anchor:   &schema.Anchor{Status: schema.AnchorResolved, Title: "loader accepts only signed bundles"},
			ProbeIDs: []string{"go-test:a_test.go:TestINV001_Holds"}},
		{ID: "INV-777", Text: "INV-777 (referenced; no bound probe from harvest)",
			Anchor: &schema.Anchor{Status: schema.AnchorOrphan}},
		{ID: "INV-004", Text: "INV-004 (referenced; no bound probe from harvest)",
			Anchor: &schema.Anchor{Status: schema.AnchorAmbiguous, Sites: []schema.Source{{File: "a.md"}, {File: "b.md"}}}},
	}
	evidence := [][]schema.Evidence{
		{{ClaimID: "INV-001", ProbeID: claims[0].ProbeIDs[0], Tier: schema.T1Assertion, Ran: true, Passed: true}},
		nil,
		nil,
	}
	r := Assemble(gitdiff.Change{BaseRef: "a", HeadRef: "b"}, claims, evidence, schema.Coverage{})

	a := r.Summary.Anchoring
	if a == nil || a.SpecIDClaims != 3 || a.Resolved != 1 || a.Ambiguous != 1 || a.Orphan != 1 {
		t.Fatalf("anchoring summary = %+v, want 3/1/1/1", a)
	}

	var text strings.Builder
	WriteText(&text, r)
	out := text.String()
	if !strings.Contains(out, "anchoring: 3 spec-id claims — 1 resolved to definitions, 1 ambiguous, 1 orphan") {
		t.Errorf("text receipt missing anchoring summary:\n%s", out)
	}
	if !strings.Contains(out, "[orphan id: defined nowhere in the spec corpus]") {
		t.Errorf("text receipt missing orphan marker:\n%s", out)
	}
	if !strings.Contains(out, "[ambiguous id: 2 definitions in the corpus]") {
		t.Errorf("text receipt missing ambiguous marker:\n%s", out)
	}
	if strings.Contains(out, "signed bundles  [") {
		t.Errorf("resolved claim carries a marker:\n%s", out)
	}

	var md strings.Builder
	WriteMarkdown(&md, r)
	if !strings.Contains(md.String(), "Anchoring: 1 of 3 spec-id claims resolved to definitions · 1 ambiguous · 1 orphan") {
		t.Errorf("markdown receipt missing anchoring line:\n%s", md.String())
	}
	if !strings.Contains(md.String(), "[orphan id: defined nowhere in the spec corpus]") {
		t.Errorf("markdown receipt missing orphan marker:\n%s", md.String())
	}

	// No anchors, no anchoring line.
	r2 := Assemble(gitdiff.Change{}, []schema.Claim{{ID: "TestFoo", Text: "t"}}, nil, schema.Coverage{})
	if r2.Summary.Anchoring != nil {
		t.Errorf("anchoring summary = %+v, want nil without anchors", r2.Summary.Anchoring)
	}
	var t2 strings.Builder
	WriteText(&t2, r2)
	if strings.Contains(t2.String(), "anchoring:") {
		t.Errorf("text receipt renders anchoring line without a corpus:\n%s", t2.String())
	}
}

// TestLLMProposedMarker: an LLM-proposed claim is marked wherever it renders
// — a reader must never mistake a model's proposal for something the change
// wrote down.
func TestLLMProposedMarker(t *testing.T) {
	claims := []schema.Claim{{
		ID: "LLM:pkg/gate.go:1", Shape: schema.ShapeInvariant,
		Text:   "The gate rejects nil input.",
		Source: schema.Source{Kind: schema.SourceLLM, File: "pkg/gate.go"},
	}}
	r := Assemble(gitdiff.Change{BaseRef: "a", HeadRef: "b"}, claims,
		[][]schema.Evidence{nil}, schema.Coverage{})
	if r.Summary.Unverified != 1 {
		t.Fatalf("summary = %+v, want the proposal in the remainder", r.Summary)
	}
	var text, md strings.Builder
	WriteText(&text, r)
	WriteMarkdown(&md, r)
	for name, out := range map[string]string{"text": text.String(), "markdown": md.String()} {
		if !strings.Contains(out, "[llm-proposed]") {
			t.Errorf("%s receipt missing the llm-proposed marker:\n%s", name, out)
		}
	}
}
