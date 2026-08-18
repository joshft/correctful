package intake

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

const goodDigest = "a41e90b65deb1111111111111111111111111111111111111111111111111111"

// write puts content at dir/name and returns the full path.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func configFor(docPath string, required bool) string {
	return fmt.Sprintf(`{
  "intake_version": 1,
  "suppliers": [
    {"name": "dafny-worker", "mechanism": "dafny-proof", "max_tier": 4,
     "document": %q, "required": %v}
  ]
}`, docPath, required)
}

func docFor(supplier, headSHA, inputDigest, rows string) string {
	return fmt.Sprintf(`{
  "intake_version": 1,
  "supplier": %q,
  "subject": {"head_sha": %q, "input_digest": %q},
  "results": [%s]
}`, supplier, headSHA, inputDigest, rows)
}

func testClaims() []schema.Claim {
	return []schema.Claim{
		{ID: "INV-009", Shape: schema.ShapeInvariant, Text: "the gate holds"},
		{ID: "INV-777", Shape: schema.ShapeInvariant, Text: "ambiguous id",
			Anchor: &schema.Anchor{Status: schema.AnchorAmbiguous}},
	}
}

// TestConfigValidatesLoudly: the authority grant fails loudly on every
// malformed shape — including a profile claiming a built-in mechanism, the
// masquerade the contract must make impossible.
func TestConfigValidatesLoudly(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()

	good := write(t, outside, "intake.json", configFor(filepath.Join(outside, "doc.json"), false))
	if _, err := LoadConfig(good, repo); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	bad := map[string]string{
		"wrong version":     `{"intake_version": 2, "suppliers": [{"name": "a", "mechanism": "b", "max_tier": 1, "document": "x"}]}`,
		"no suppliers":      `{"intake_version": 1, "suppliers": []}`,
		"bad name":          `{"intake_version": 1, "suppliers": [{"name": "Not Token", "mechanism": "b", "max_tier": 1, "document": "x"}]}`,
		"builtin mechanism": `{"intake_version": 1, "suppliers": [{"name": "a", "mechanism": "go-test", "max_tier": 1, "document": "x"}]}`,
		"tier out of range": `{"intake_version": 1, "suppliers": [{"name": "a", "mechanism": "b", "max_tier": 5, "document": "x"}]}`,
		"no document":       `{"intake_version": 1, "suppliers": [{"name": "a", "mechanism": "b", "max_tier": 1, "document": ""}]}`,
		"unknown field":     `{"intake_version": 1, "surprise": true, "suppliers": [{"name": "a", "mechanism": "b", "max_tier": 1, "document": "x"}]}`,
		"duplicate name":    `{"intake_version": 1, "suppliers": [{"name": "a", "mechanism": "b", "max_tier": 1, "document": "x"}, {"name": "a", "mechanism": "c", "max_tier": 1, "document": "y"}]}`,
	}
	for name, content := range bad {
		p := write(t, outside, "bad-"+strings.ReplaceAll(name, " ", "-")+".json", content)
		if _, err := LoadConfig(p, repo); err == nil {
			t.Errorf("%s: loaded without error — a broken authority grant must fail loudly", name)
		}
	}
}

// TestInTreePathsRejected: evidence the reviewed change can write is not
// evidence. Config and documents inside the repo tree are refused, and a
// symlinked path is refused even when its target lies outside.
func TestInTreePathsRejected(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()

	inTree := write(t, repo, "intake.json", configFor(filepath.Join(outside, "doc.json"), false))
	if _, err := LoadConfig(inTree, repo); err == nil || !strings.Contains(err.Error(), "inside the repository tree") {
		t.Errorf("in-tree config: %v — want inside-the-repository rejection", err)
	}

	target := write(t, outside, "real.json", configFor(filepath.Join(outside, "doc.json"), false))
	link := filepath.Join(outside, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(link, repo); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlink config: %v — want symlink rejection", err)
	}

	// A document inside the tree is an error even when the config is fine.
	inTreeDoc := write(t, repo, "doc.json", docFor("dafny-worker", "abc", goodDigest, ""))
	cfgPath := write(t, outside, "cfg2.json", configFor(inTreeDoc, false))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Run(cfg, repo, Subject{HeadSHA: "abc", InputDigest: goodDigest}, testClaims()); err == nil {
		t.Error("in-tree document admitted — the reviewed change must not supply its own evidence")
	}
}

// TestAdmissionGates: a document is admitted only when its supplier matches
// the profile and its subject matches the receipt EXACTLY. Everything else
// is recorded with a reason — and a REQUIRED profile with nothing admitted
// blocks the gate through GateBlocked.
func TestAdmissionGates(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	subj := Subject{HeadSHA: "abc123", InputDigest: goodDigest}
	row := `{"claim_id": "INV-009", "probe_id": "specs/gate.dfy:GateSafe", "outcome": "verified", "detail": "proof ok", "duration": "12s"}`

	cases := []struct {
		name, doc  string
		wantReason string
	}{
		{"admitted", docFor("dafny-worker", "abc123", goodDigest, row), ""},
		{"wrong supplier", docFor("other-tool", "abc123", goodDigest, row), "names supplier"},
		{"stale head", docFor("dafny-worker", "def456", goodDigest, row), "subject mismatch"},
		{"stale digest", docFor("dafny-worker", "abc123", strings.Repeat("b", 64), row), "subject mismatch"},
		{"malformed digest", docFor("dafny-worker", "abc123", "SHORT", row), "not a canonical sha256"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docPath := write(t, outside, "doc-"+strings.ReplaceAll(tc.name, " ", "-")+".json", tc.doc)
			cfgPath := write(t, outside, "cfg-"+strings.ReplaceAll(tc.name, " ", "-")+".json", configFor(docPath, true))
			cfg, err := LoadConfig(cfgPath, repo)
			if err != nil {
				t.Fatal(err)
			}
			extra, records, err := Run(cfg, repo, subj, testClaims())
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("records = %d, want 1 (every profile yields a record)", len(records))
			}
			rec := records[0]
			if tc.wantReason == "" {
				if !rec.Admitted || rec.Accepted != 1 || len(extra["INV-009"]) != 1 {
					t.Fatalf("admission failed: %+v / %v", rec, extra)
				}
				wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(tc.doc)))
				if rec.DocDigest != wantDigest {
					t.Errorf("doc digest = %s, want sha256 of the exact bytes", rec.DocDigest)
				}
				return
			}
			if rec.Admitted || !strings.Contains(rec.Reason, tc.wantReason) {
				t.Errorf("record = %+v, want rejection containing %q", rec, tc.wantReason)
			}
			r := schema.Receipt{Intake: records}
			if !r.GateBlocked() {
				t.Error("required profile with nothing admitted did not block the gate")
			}
		})
	}
}

// TestAuthorityComesFromTheProfile: the row cannot select tier, mechanism,
// or supplier — accepted evidence carries the PROFILE's authority, the
// constructed ext: namespace, and the supplier-attested binding.
func TestAuthorityComesFromTheProfile(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	subj := Subject{HeadSHA: "abc", InputDigest: goodDigest}
	row := `{"claim_id": "INV-009", "probe_id": "specs/gate.dfy:GateSafe", "outcome": "verified"}`
	docPath := write(t, outside, "doc.json", docFor("dafny-worker", "abc", goodDigest, row))
	cfgPath := write(t, outside, "cfg.json", configFor(docPath, false))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	extra, _, err := Run(cfg, repo, subj, testClaims())
	if err != nil {
		t.Fatal(err)
	}
	ev := extra["INV-009"][0]
	if ev.Tier != schema.T4Mechanical || ev.Mechanism != "dafny-proof" || ev.Supplier != "dafny-worker" {
		t.Errorf("authority = %+v, want the profile's tier/mechanism/supplier", ev)
	}
	if ev.ProbeID != "ext:dafny-worker/specs/gate.dfy:GateSafe" {
		t.Errorf("probe id = %q, want the constructed ext: namespace", ev.ProbeID)
	}
	if ev.Binding != schema.BindingSupplierAttested {
		t.Errorf("binding = %q, want supplier-attested", ev.Binding)
	}
	if !ev.CountsFor(testClaims()[0]) {
		t.Error("admitted verified row does not count for its claim")
	}
}

// TestOutcomeVocabulary: only "counterexample" refutes. "inconclusive",
// "not_run", and "error" are Ran=false — an unproved proof or a fuzzer
// timeout must never read as a refutation. Unknown outcomes are rejected.
func TestOutcomeVocabulary(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	subj := Subject{HeadSHA: "abc", InputDigest: goodDigest}
	cases := []struct {
		outcome             string
		wantRan, wantPassed bool
		rejected            bool
	}{
		{OutcomeVerified, true, true, false},
		{OutcomeCounterexample, true, false, false},
		{OutcomeInconclusive, false, false, false},
		{OutcomeNotRun, false, false, false},
		{OutcomeError, false, false, false},
		{"passed", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.outcome, func(t *testing.T) {
			row := fmt.Sprintf(`{"claim_id": "INV-009", "probe_id": "p", "outcome": %q, "detail": "d"}`, tc.outcome)
			docPath := write(t, outside, "doc-"+tc.outcome+".json", docFor("dafny-worker", "abc", goodDigest, row))
			cfgPath := write(t, outside, "cfg-"+tc.outcome+".json", configFor(docPath, false))
			cfg, err := LoadConfig(cfgPath, repo)
			if err != nil {
				t.Fatal(err)
			}
			extra, records, err := Run(cfg, repo, subj, testClaims())
			if err != nil {
				t.Fatal(err)
			}
			if tc.rejected {
				if len(extra) != 0 || len(records[0].Rejected) != 1 {
					t.Fatalf("unknown outcome not rejected: %v / %+v", extra, records)
				}
				return
			}
			ev := extra["INV-009"][0]
			if ev.Ran != tc.wantRan || ev.Passed != tc.wantPassed {
				t.Errorf("%s → ran=%v passed=%v, want %v/%v", tc.outcome, ev.Ran, ev.Passed, tc.wantRan, tc.wantPassed)
			}
			if ev.Refuted() != (tc.outcome == OutcomeCounterexample) {
				t.Errorf("%s: Refuted() = %v — only a counterexample refutes", tc.outcome, ev.Refuted())
			}
		})
	}
}

// TestRowRejections: rows naming unknown claims, ambiguously anchored
// claims, or duplicate probes are rejected WITH the row's identity and
// reason — never reduced to a bare count, because a rejected counterexample
// can expose claim drift.
func TestRowRejections(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	subj := Subject{HeadSHA: "abc", InputDigest: goodDigest}
	rows := strings.Join([]string{
		`{"claim_id": "INV-999", "probe_id": "p1", "outcome": "counterexample", "detail": "boom"}`,
		`{"claim_id": "INV-777", "probe_id": "p2", "outcome": "verified"}`,
		`{"claim_id": "INV-009", "probe_id": "p3", "outcome": "verified"}`,
		`{"claim_id": "INV-009", "probe_id": "p3", "outcome": "verified"}`,
	}, ",")
	docPath := write(t, outside, "doc.json", docFor("dafny-worker", "abc", goodDigest, rows))
	cfgPath := write(t, outside, "cfg.json", configFor(docPath, false))
	cfg, err := LoadConfig(cfgPath, repo)
	if err != nil {
		t.Fatal(err)
	}
	extra, records, err := Run(cfg, repo, subj, testClaims())
	if err != nil {
		t.Fatal(err)
	}
	rec := records[0]
	if rec.Accepted != 1 || len(extra["INV-009"]) != 1 {
		t.Fatalf("accepted = %d, want exactly the one clean row", rec.Accepted)
	}
	if len(rec.Rejected) != 3 {
		t.Fatalf("rejected = %+v, want 3 rows with reasons", rec.Rejected)
	}
	reasons := map[string]string{}
	for _, rej := range rec.Rejected {
		reasons[rej.ClaimID+"/"+rej.ProbeID] = rej.Reason
	}
	if !strings.Contains(reasons["INV-999/p1"], "no such claim") {
		t.Errorf("unknown claim reason = %q", reasons["INV-999/p1"])
	}
	if !strings.Contains(reasons["INV-777/p2"], "ambiguously anchored") {
		t.Errorf("ambiguous anchor reason = %q", reasons["INV-777/p2"])
	}
	if !strings.Contains(reasons["INV-009/p3"], "duplicate") {
		t.Errorf("duplicate reason = %q", reasons["INV-009/p3"])
	}
	for _, rej := range rec.Rejected {
		if rej.ClaimID == "INV-999" && rej.Outcome != OutcomeCounterexample {
			t.Error("rejected counterexample lost its outcome — claim drift must stay visible")
		}
	}
}
