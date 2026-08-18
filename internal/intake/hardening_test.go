package intake

// Regression tests for the adversarial verification findings: each test
// here pins a CONFIRMED hole from the post-implementation review of the
// intake contract.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

// TestContradictoryVerdictsFailLoudly: a supplier that reports both a pass
// and a counterexample for the same probe is a supplier bug — silently
// keeping either verdict could launder the other away, so the run errors.
func TestContradictoryVerdictsFailLoudly(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	rows := `{"claim_id": "INV-009", "probe_id": "p", "outcome": "verified"},
		{"claim_id": "INV-009", "probe_id": "p", "outcome": "counterexample"}`
	docPath := write(t, outside, "doc.json", docFor("dafny-worker", "abc", goodDigest, rows))
	cfgPath := write(t, outside, "cfg.json", configFor(docPath, false))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Run(cfg, repo, Subject{HeadSHA: "abc", InputDigest: goodDigest}, testClaims()); err == nil ||
		!strings.Contains(err.Error(), "contradictory verdicts") {
		t.Errorf("contradiction tolerated: %v", err)
	}
}

// TestCrossSupplierCounterexampleSurvives: refutation dominance across
// suppliers. Supplier A's pass on a raw probe id must NOT suppress supplier
// B's counterexample on the same raw id — the namespaced probes are
// distinct evidence, and the demonstrated global-dedupe hole let a pass
// launder a refutation away.
func TestCrossSupplierCounterexampleSurvives(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	subj := Subject{HeadSHA: "abc", InputDigest: goodDigest}
	passDoc := write(t, outside, "a.json", docFor("prover-a", "abc", goodDigest,
		`{"claim_id": "INV-009", "probe_id": "shared", "outcome": "verified"}`))
	cxDoc := write(t, outside, "b.json", docFor("prover-b", "abc", goodDigest,
		`{"claim_id": "INV-009", "probe_id": "shared", "outcome": "counterexample", "detail": "trace"}`))
	cfgPath := write(t, outside, "cfg.json", fmt.Sprintf(`{
  "intake_version": 1,
  "suppliers": [
    {"name": "prover-a", "mechanism": "proof-a", "max_tier": 4, "document": %q},
    {"name": "prover-b", "mechanism": "proof-b", "max_tier": 4, "document": %q}
  ]
}`, passDoc, cxDoc))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	extra, records, err := Run(cfg, repo, subj, testClaims())
	if err != nil {
		t.Fatal(err)
	}
	if records[0].Accepted != 1 || records[1].Accepted != 1 {
		t.Fatalf("accepted = %d/%d, want both suppliers' rows: %+v", records[0].Accepted, records[1].Accepted, records)
	}
	rows := extra["INV-009"]
	if len(rows) != 2 {
		t.Fatalf("evidence rows = %d, want 2 distinct namespaced probes", len(rows))
	}
	refuted := false
	for _, ev := range rows {
		if ev.Refuted() {
			refuted = true
		}
	}
	if !refuted {
		t.Error("the counterexample was suppressed — refutation dominance violated")
	}
}

// TestDuplicateJSONKeysRejected: the stdlib decoder keeps a duplicate
// key's last value — demonstrated to smuggle "outcome": "verified" behind
// a counterexample — so strict decoding refuses duplicates at any depth.
func TestDuplicateJSONKeysRejected(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	doc := `{
  "intake_version": 1,
  "supplier": "dafny-worker",
  "subject": {"head_sha": "abc", "input_digest": "` + goodDigest + `"},
  "results": [
    {"claim_id": "INV-009", "probe_id": "p", "outcome": "counterexample", "outcome": "verified"}
  ]
}`
	docPath := write(t, outside, "doc.json", doc)
	cfgPath := write(t, outside, "cfg.json", configFor(docPath, false))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Run(cfg, repo, Subject{HeadSHA: "abc", InputDigest: goodDigest}, testClaims()); err == nil ||
		!strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("duplicate key tolerated: %v", err)
	}
}

// TestParentSymlinkCannotSmuggleInTreeFiles: containment is CANONICAL. A
// symlinked parent directory that resolves into the repository tree was
// demonstrated to pass a lexical prefix check; the resolved path is what
// the boundary judges.
func TestParentSymlinkCannotSmuggleInTreeFiles(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "evil"), "doc.json", docFor("dafny-worker", "abc", goodDigest,
		`{"claim_id": "INV-009", "probe_id": "p", "outcome": "verified"}`))
	linkDir := filepath.Join(outside, "looks-external")
	if err := os.Symlink(filepath.Join(repo, "evil"), linkDir); err != nil {
		t.Fatal(err)
	}
	cfgPath := write(t, outside, "cfg.json", configFor(filepath.Join(linkDir, "doc.json"), false))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Run(cfg, repo, Subject{HeadSHA: "abc", InputDigest: goodDigest}, testClaims()); err == nil ||
		!strings.Contains(err.Error(), "inside the repository tree") {
		t.Errorf("parent symlink smuggled an in-tree document: %v", err)
	}
}

// TestRequiredNeedsUsableEvidence: an admitted document with zero accepted
// rows — empty, or every row rejected — satisfies nothing. Required means
// usable evidence arrived.
func TestRequiredNeedsUsableEvidence(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	subj := Subject{HeadSHA: "abc", InputDigest: goodDigest}
	for name, rows := range map[string]string{
		"empty":        "",
		"all rejected": `{"claim_id": "INV-404", "probe_id": "p", "outcome": "verified"}`,
	} {
		docPath := write(t, outside, "doc-"+strings.ReplaceAll(name, " ", "-")+".json",
			docFor("dafny-worker", "abc", goodDigest, rows))
		cfgPath := write(t, outside, "cfg-"+strings.ReplaceAll(name, " ", "-")+".json", configFor(docPath, true))
		cfg, err := LoadConfig(cfgPath, repo)
		if err != nil {
			t.Fatal(err)
		}
		_, records, err := Run(cfg, repo, subj, testClaims())
		if err != nil {
			t.Fatal(err)
		}
		r := schema.Receipt{Intake: records}
		if !r.GateBlocked() {
			t.Errorf("%s: required document with nothing usable did not block", name)
		}
	}
}

// TestRejectedRowsAreScrubbed: rejection fields render in receipts, so
// control characters (an ESC sequence, demonstrated live; DEL and C1 are
// covered by the same scrub) must not survive into them — and the audit
// record carries the supplier version and the config digest. The ESC
// arrives through JSON's \u001b escape, exactly as a hostile document
// would deliver it.
func TestRejectedRowsAreScrubbed(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	rows := `{"claim_id": "INV-\u001b[31m999", "probe_id": "p1", "outcome": "verified"}`
	docPath := write(t, outside, "doc.json", docFor("dafny-worker", "abc", goodDigest, rows))
	cfgPath := write(t, outside, "cfg.json", strings.Replace(configFor(docPath, false),
		`"mechanism"`, `"version": "1.2.0", "mechanism"`, 1))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	_, records, err := Run(cfg, repo, Subject{HeadSHA: "abc", InputDigest: goodDigest}, testClaims())
	if err != nil {
		t.Fatal(err)
	}
	rec := records[0]
	if len(rec.Rejected) != 1 {
		t.Fatalf("rejected = %+v", rec.Rejected)
	}
	for _, s := range []string{rec.Rejected[0].ClaimID, rec.Rejected[0].ProbeID} {
		for _, r := range s {
			if r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
				t.Errorf("control character %U survived in %q", r, s)
			}
		}
	}
	if !strings.Contains(rec.Rejected[0].ClaimID, "INV-") || !strings.Contains(rec.Rejected[0].ClaimID, "999") {
		t.Errorf("scrub destroyed the printable content: %q", rec.Rejected[0].ClaimID)
	}
	if rec.SupplierVersion != "1.2.0" {
		t.Errorf("supplier version = %q, want the profile's declaration", rec.SupplierVersion)
	}
	if rec.ConfigDigest == "" {
		t.Error("config digest absent — the authority file is unpinned")
	}
}

// TestCaseVariantOutcomeKeyRejected: the CRITICAL live-path hole. An intake
// row carrying both "outcome" and "Outcome" passed exact-match duplicate
// detection, and Go's case-insensitive field matching then read the second
// one — turning a counterexample into a pass and defeating refutation
// dominance at the external boundary. The strict decoder now rejects the
// case-fold collision, so the whole document fails loudly.
func TestCaseVariantOutcomeKeyRejected(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	rows := `{"claim_id": "INV-009", "probe_id": "p", "outcome": "counterexample", "Outcome": "verified"}`
	docPath := write(t, outside, "doc.json", docFor("dafny-worker", "abc", goodDigest, rows))
	cfgPath := write(t, outside, "cfg.json", configFor(docPath, false))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Run(cfg, repo, Subject{HeadSHA: "abc", InputDigest: goodDigest}, testClaims())
	if err == nil || !strings.Contains(err.Error(), "case-variant key collision") {
		t.Errorf("case-variant outcome key tolerated: %v", err)
	}
}
