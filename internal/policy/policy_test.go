package policy

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

// validPolicy is a real-shaped policy file: one named adversarial floor over
// a subtree, one plain T1 floor over a glob.
const validPolicy = `{
  "policy_version": 1,
  "rules": [
    {"name": "auth-floor", "paths": ["internal/auth/..."], "min_tier": 2, "mechanism": "go-test-pair"},
    {"paths": ["pkg/*.go"], "min_tier": 1}
  ]
}`

func writePolicy(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestLoadValidatesAndDigests: a missing file is no policy; a valid file
// parses with a digest over its exact bytes; every malformed shape fails
// LOUDLY — a broken floor must never fail open.
func TestLoadValidatesAndDigests(t *testing.T) {
	if p, err := Load(t.TempDir()); p != nil || err != nil {
		t.Fatalf("missing file: %v, %v — want no policy, no error", p, err)
	}

	p, err := Load(writePolicy(t, validPolicy))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) != 2 || p.Rules[0].Name != "auth-floor" {
		t.Fatalf("parsed rules = %+v", p.Rules)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte(validPolicy))); p.digest != want {
		t.Errorf("digest = %s, want sha256 of the exact bytes", p.digest)
	}

	bad := map[string]string{
		"not json":            `{"policy_version": 1,`,
		"wrong version":       `{"policy_version": 2, "rules": [{"paths": ["x"], "min_tier": 1}]}`,
		"no rules":            `{"policy_version": 1, "rules": []}`,
		"rule without path":   `{"policy_version": 1, "rules": [{"paths": [], "min_tier": 1}]}`,
		"tier out of range":   `{"policy_version": 1, "rules": [{"paths": ["x"], "min_tier": 5}]}`,
		"malformed mechanism": `{"policy_version": 1, "rules": [{"paths": ["x"], "min_tier": 1, "mechanism": "Not A Token!"}]}`,
		"unknown scope":       `{"policy_version": 1, "rules": [{"paths": ["x"], "min_tier": 1, "scope": "galaxy"}]}`,
	}
	for name, content := range bad {
		if _, err := Load(writePolicy(t, content)); err == nil {
			t.Errorf("%s: loaded without error — a broken floor must fail loudly", name)
		}
	}

	// The mechanism set is OPEN: a floor may require an external supplier's
	// mechanism (declared in the invoker's intake config). A typo fails
	// closed as an unsatisfiable floor, never as a load error.
	external := `{"policy_version": 1, "rules": [{"paths": ["x"], "min_tier": 4, "mechanism": "dafny-proof"}]}`
	if _, err := Load(writePolicy(t, external)); err != nil {
		t.Errorf("external mechanism rejected at load: %v", err)
	}
}

// TestPatternMatching: exact paths, "dir/..." subtrees, and path.Match
// globs whose * never crosses a slash.
func TestPatternMatching(t *testing.T) {
	cases := []struct {
		pattern, file string
		want          bool
	}{
		{"internal/auth/gate.go", "internal/auth/gate.go", true},
		{"internal/auth/...", "internal/auth/gate.go", true},
		{"internal/auth/...", "internal/auth/deep/nested.go", true},
		{"internal/auth/...", "internal/authz/other.go", false},
		{"pkg/*.go", "pkg/gate.go", true},
		{"pkg/*.go", "pkg/sub/gate.go", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/notes.md", false},
	}
	for _, tc := range cases {
		if got := matchesPattern(tc.pattern, tc.file); got != tc.want {
			t.Errorf("matchesPattern(%q, %q) = %v, want %v", tc.pattern, tc.file, got, tc.want)
		}
	}
}

// receiptWith builds a minimal assembled receipt for evaluation tests.
func receiptWith(files []string, results []schema.ClaimResult) schema.Receipt {
	return schema.Receipt{Change: schema.ChangeRef{Files: files}, Results: results}
}

func verifiedResult(claim schema.Claim, ev schema.Evidence) schema.ClaimResult {
	return schema.ClaimResult{Claim: claim, Status: schema.StatusVerified,
		EffectiveTier: ev.Tier, Evidence: []schema.Evidence{ev}}
}

// TestEvaluateFloors: the per-file semantics. A matched file needs one
// verified claim that TIES to it (source file or reference site) with
// evidence meeting the floor; test files are exempt and counted; untied
// files miss with the exact reason; under-floor evidence misses naming the
// best tied evidence.
func TestEvaluateFloors(t *testing.T) {
	p := &Policy{PolicyVersion: 1, digest: "d", Rules: []Rule{
		{Name: "auth-floor", Paths: []string{"internal/auth/..."}, MinTier: 2, Mechanism: schema.MechanismGoTestPair},
	}}

	pairEv := schema.Evidence{Tier: schema.T2Adversarial, Ran: true, Passed: true,
		Mechanism: schema.MechanismGoTestPair}
	t1Ev := schema.Evidence{Tier: schema.T1Assertion, Ran: true, Passed: true,
		Mechanism: schema.MechanismGoTest}

	refClaim := schema.Claim{ID: "INV-001", Source: schema.Source{Kind: schema.SourceGoTest, File: "internal/auth/gate_test.go"},
		RefSites: []schema.Source{{File: "internal/auth/gate.go", Line: 3}}}

	// Satisfied via a reference-site tie with pair evidence.
	res := Evaluate(p, receiptWith(
		[]string{"internal/auth/gate.go", "internal/auth/gate_test.go"},
		[]schema.ClaimResult{verifiedResult(refClaim, pairEv)}))
	if len(res.Misses) != 0 {
		t.Errorf("satisfied floor produced misses: %+v", res.Misses)
	}
	if res.ExemptTestFiles != 1 {
		t.Errorf("exempt test files = %d, want 1", res.ExemptTestFiles)
	}
	if res.Digest != "d" || res.Rules != 1 {
		t.Errorf("result header = %+v", res)
	}

	// Under-floor: tied evidence exists but is T1 single-test.
	res = Evaluate(p, receiptWith(
		[]string{"internal/auth/gate.go"},
		[]schema.ClaimResult{verifiedResult(refClaim, t1Ev)}))
	if len(res.Misses) != 1 || !strings.Contains(res.Misses[0].Detail, "best tied evidence is T1-assertion go-test") ||
		!strings.Contains(res.Misses[0].Detail, "≥T2-adversarial go-test-pair") {
		t.Errorf("under-floor miss = %+v", res.Misses)
	}
	if res.Misses[0].Rule != "auth-floor" {
		t.Errorf("rule label = %q", res.Misses[0].Rule)
	}

	// No tie at all: verified claims elsewhere do not speak for this file.
	elsewhere := schema.Claim{ID: "X", Source: schema.Source{File: "other/thing_test.go"}}
	res = Evaluate(p, receiptWith(
		[]string{"internal/auth/gate.go"},
		[]schema.ClaimResult{verifiedResult(elsewhere, pairEv)}))
	if len(res.Misses) != 1 || !strings.Contains(res.Misses[0].Detail, "no verified claim ties to this file") {
		t.Errorf("untied miss = %+v", res.Misses)
	}

	// Unmatched files are not policed.
	res = Evaluate(p, receiptWith([]string{"README.md"}, nil))
	if len(res.Misses) != 0 {
		t.Errorf("unmatched file policed: %+v", res.Misses)
	}
}

// TestEvaluateScopeFloorAndLLMGate: a scope floor demands measured
// cross-package evidence, and the LLM edge gate applies IDENTICALLY here as
// in weighing (Evidence.CountsFor): a pass on an unconfirmed model-proposed
// edge satisfies no floor.
func TestEvaluateScopeFloorAndLLMGate(t *testing.T) {
	p := &Policy{PolicyVersion: 1, digest: "d", Rules: []Rule{
		{Paths: []string{"svc/..."}, MinTier: 1, Scope: schema.ScopeCrossPackage},
	}}

	llmClaim := schema.Claim{ID: "LLM:svc/api.go:1", Source: schema.Source{Kind: schema.SourceLLM, File: "svc/api.go"}}
	confirmedCross := schema.Evidence{Tier: schema.T1Assertion, Ran: true, Passed: true,
		Mechanism: schema.MechanismGoTest, Scope: schema.ScopeCrossPackage, Binding: schema.BindingFileCovered}
	unconfirmedCross := confirmedCross
	unconfirmedCross.Binding = ""
	confirmedSingle := confirmedCross
	confirmedSingle.Scope = schema.ScopeSinglePackage

	res := Evaluate(p, receiptWith([]string{"svc/api.go"},
		[]schema.ClaimResult{verifiedResult(llmClaim, confirmedCross)}))
	if len(res.Misses) != 0 {
		t.Errorf("confirmed cross-package edge missed the floor: %+v", res.Misses)
	}

	res = Evaluate(p, receiptWith([]string{"svc/api.go"},
		[]schema.ClaimResult{verifiedResult(llmClaim, unconfirmedCross)}))
	if len(res.Misses) != 1 {
		t.Errorf("unconfirmed llm edge satisfied a floor: %+v", res.Misses)
	}

	res = Evaluate(p, receiptWith([]string{"svc/api.go"},
		[]schema.ClaimResult{verifiedResult(llmClaim, confirmedSingle)}))
	if len(res.Misses) != 1 || !strings.Contains(res.Misses[0].Detail, "single-package") {
		t.Errorf("single-package vs cross floor = %+v", res.Misses)
	}
}
