package receipt

import (
	"bytes"
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/schema"
)

func TestSerializedGateMatchesComputedOutcome(t *testing.T) {
	for _, kind := range []string{"pass", "remainder", "refuted", "policy", "required-intake", "empty-intake", "optional-intake"} {
		t.Run(kind, func(t *testing.T) {
			claim := schema.Claim{ID: "test-claim"}
			ev := schema.Evidence{ClaimID: "test-claim", Tier: 1, Ran: true, Passed: true}
			switch kind {
			case "refuted":
				ev.Passed = false
			case "remainder":
				ev.Ran = false
			}
			r := Assemble(gitdiff.Change{Repo: "example", Files: []string{"example.go"}}, []schema.Claim{claim}, [][]schema.Evidence{{ev}}, schema.Coverage{Files: []schema.FileCoverage{{File: "example.go", Claims: 1}}, Claimed: 1})
			want := "pass"
			switch kind {
			case "refuted":
				want = "refuted"
				r.Policy = &schema.PolicyResult{Digest: strings.Repeat("a", 64), Misses: []schema.PolicyMiss{{File: "example.go", Rule: "floor"}}}
			case "policy":
				want = "blocked"
				r.Policy = &schema.PolicyResult{Digest: strings.Repeat("a", 64), Misses: []schema.PolicyMiss{{File: "example.go", Rule: "floor"}}}
			case "required-intake", "empty-intake":
				want = "blocked"
				r.Intake = []schema.IntakeRecord{{Supplier: "supplier", MaxTier: 1, Required: true, Admitted: kind == "empty-intake"}}
			case "optional-intake":
				r.Intake = []schema.IntakeRecord{{Supplier: "supplier", MaxTier: 1}}
			}
			r.Gate = r.GateVerdict()
			if r.Gate != want {
				t.Fatalf("got %s, want %s", r.Gate, want)
			}
			if err := ValidateConsistency(r); err != nil {
				t.Fatal(err)
			}
			raw, err := Canonical(r)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"gate": "`+want+`"`)) {
				t.Fatal("serialized verdict missing")
			}
			r.Gate = "invented"
			if err := ValidateConsistency(r); err == nil {
				t.Fatal("unrecognized verdict accepted")
			}
		})
	}
}

func TestProducerRenderingIsInertAndUsesStoredVerdict(t *testing.T) {
	r := schema.Receipt{Producer: &schema.ReceiptProducer{Runner: "runner\n## forged", Role: "gate\x1b"}, Gate: "blocked\n## forged"}
	for _, render := range []func(*bytes.Buffer){
		func(w *bytes.Buffer) { WriteText(w, r) }, func(w *bytes.Buffer) { WriteMarkdown(w, r) },
	} {
		var out bytes.Buffer
		render(&out)
		if strings.Contains(out.String(), "\n## forged") || strings.Contains(out.String(), "\x1b") {
			t.Fatal("active display content")
		}
		if !strings.Contains(out.String(), "blocked") {
			t.Fatal("stored verdict was recomputed from empty results")
		}
	}
}
