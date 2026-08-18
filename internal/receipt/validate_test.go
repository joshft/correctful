package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/schema"
)

func consistentReceipt(t *testing.T) schema.Receipt {
	t.Helper()
	claims, evidence := sampleClaims()
	cov := schema.Coverage{
		Files: []schema.FileCoverage{
			{File: "x.go", ReadBy: []string{"gotest"}, Claims: 2},
			{File: "y.go", ReadBy: []string{"spec-ref"}, Claims: 1},
			{File: "z.bin", SkipReason: "no-harvester"},
			{File: ".ci/tool.cfg", SkipReason: "hidden-path"},
		},
		Claimed:      2,
		Unread:       2,
		UnreadPolicy: 1,
	}
	r := Assemble(gitdiff.Change{Repo: "repo", BaseRef: "main", HeadRef: "wip"}, claims, evidence, cov)
	r.ToolVersion = "test"
	return r
}

// TestValidateConsistencyAcceptsAssembledReceipt: whatever Assemble
// produces must validate — the validator re-derives with the same rules
// the assembler derives with, or every honest receipt would fail.
func TestValidateConsistencyAcceptsAssembledReceipt(t *testing.T) {
	if err := ValidateConsistency(consistentReceipt(t)); err != nil {
		t.Fatalf("assembled receipt rejected: %v", err)
	}
}

// TestValidateConsistencyRejectsTampering: each mutation below is a field
// a wrapper could edit after assembly and before signing; every one must
// be named and refused. The zeroed-refuted case is the codex-review attack
// — GateBlocked reads Summary.Refuted, so a signature over the tampered
// summary would otherwise carry a refutation past the gate.
func TestValidateConsistencyRejectsTampering(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*schema.Receipt)
		want   string
	}{
		{"zeroed refuted count", func(r *schema.Receipt) { r.Summary.Refuted = 0; r.Summary.Verified = 2 }, "summary arithmetic"},
		{"status flip", func(r *schema.Receipt) { r.Results[1].Status = schema.StatusVerified }, "evidence weighs to"},
		{"tier inflation", func(r *schema.Receipt) { r.Results[0].EffectiveTier = schema.T4Mechanical }, "evidence weighs to"},
		{"remainder dropped", func(r *schema.Receipt) { r.Remainder = nil }, "remainder"},
		{"tier counts", func(r *schema.Receipt) { r.Summary.TierCounts["T4-mechanical"] = 3 }, "tier count"},
		{"coverage arithmetic", func(r *schema.Receipt) { r.Coverage.Unread = 0 }, "coverage arithmetic"},
		{"alien schema", func(r *schema.Receipt) { r.SchemaVersion = "9.9.9" }, "schema"},
		{"policy digest shape", func(r *schema.Receipt) {
			r.Policy = &schema.PolicyResult{Path: "correctful.json", Digest: "not-hex", Rules: 1}
		}, "sha256"},
		{"intake accepted without admission", func(r *schema.Receipt) {
			r.Intake = []schema.IntakeRecord{{Supplier: "s", MaxTier: schema.T3Property, Accepted: 2}}
		}, "did not admit"},
	}
	for _, c := range cases {
		r := consistentReceipt(t)
		c.mutate(&r)
		err := ValidateConsistency(r)
		if err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %q does not name %q", c.name, err, c.want)
		}
	}
}

// TestCanonicalGoldenVector freezes the canonical encoding: struct-order
// keys, sorted map keys, HTML escaping, two-space indent, trailing
// newline. If this digest moves and you did not deliberately change the
// schema or the encoder, you have changed what existing signatures verify
// over — stop. On a deliberate schema change, re-pin and say so in the
// commit message.
func TestCanonicalGoldenVector(t *testing.T) {
	b, err := Canonical(consistentReceipt(t))
	if err != nil {
		t.Fatal(err)
	}
	const want = "f52a70f30f50c45bb5d51401e2d5866d34287600d8a5024aaf42a53976b43297"
	if got := hex.EncodeToString(sha256sum(b)); got != want {
		t.Fatalf("canonical encoding drifted:\n got sha256 %s\nwant sha256 %s\nfirst 200 bytes:\n%s", got, want, b[:200])
	}
	// WriteJSON must emit exactly the canonical bytes — one encoder, one
	// byte-form.
	var sb strings.Builder
	if err := WriteJSON(&sb, consistentReceipt(t)); err != nil {
		t.Fatal(err)
	}
	if sb.String() != string(b) {
		t.Fatalf("WriteJSON diverges from Canonical")
	}
}

func sha256sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
