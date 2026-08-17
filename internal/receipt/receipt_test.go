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
