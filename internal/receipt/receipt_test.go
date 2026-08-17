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

// TestT0PassConfersNoVerification: a probe result that ran and passed but
// confers only T0 raises nothing — T0 IS the unverified tier, so accepting
// such a pass would mint a verified row whose effective tier means "nothing
// checked this". The claim stays in the remainder, and the pass does not
// dilute an honest refutation either.
func TestT0PassConfersNoVerification(t *testing.T) {
	claims := []schema.Claim{{ID: "Z1", Shape: schema.ShapeInvariant,
		ProbeIDs: []string{"noop:x"}}}
	evidence := [][]schema.Evidence{{
		{ClaimID: "Z1", ProbeID: "noop:x", Tier: schema.T0Unverified, Ran: true, Passed: true},
	}}
	r := Assemble(gitdiff.Change{}, claims, evidence, schema.Coverage{})
	if r.Results[0].Status != schema.StatusUnverified {
		t.Fatalf("status = %q, want unverified (a T0 pass confers nothing)", r.Results[0].Status)
	}
	if len(r.Remainder) != 1 || r.Remainder[0].Claim.ID != "Z1" {
		t.Fatalf("claim with only a T0 pass must land in the remainder")
	}
	if r.Summary.Verified != 0 {
		t.Fatalf("summary.Verified = %d, want 0", r.Summary.Verified)
	}
}

// TestScopeExclusionsAndInputDigestAreDisclosed: the change resolver's
// deliberate scope cuts never reach the harvest, so BOTH renderers must state
// them at the scope boundary itself; the input digest joins the SHA pins so a
// dirty-tree receipt is identifiable.
func TestScopeExclusionsAndInputDigestAreDisclosed(t *testing.T) {
	r := Assemble(gitdiff.Change{
		BaseRef: "main", HeadRef: "wip",
		Excluded: []gitdiff.Exclusion{
			{Reason: "untracked-territory", Count: 2136, Dirs: []string{"toolcache"}},
			{Reason: "untracked-hidden", Count: 3},
		},
		InputDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, nil, nil, schema.Coverage{})

	for name, render := range map[string]func(*strings.Builder){
		"markdown": func(b *strings.Builder) { WriteMarkdown(b, r) },
		"text":     func(b *strings.Builder) { WriteText(b, r) },
	} {
		var b strings.Builder
		render(&b)
		out := b.String()
		if !strings.Contains(out, "2136 untracked file(s) in never-tracked top-level trees (toolcache)") {
			t.Errorf("%s receipt omits the territory exclusion:\n%s", name, out)
		}
		if !strings.Contains(out, "3 hidden untracked file(s)") {
			t.Errorf("%s receipt omits the hidden exclusion:\n%s", name, out)
		}
		if !strings.Contains(out, "input:0123456789ab") {
			t.Errorf("%s receipt omits the input digest pin:\n%s", name, out)
		}
	}

	if r.Change.Excluded[0].Count != 2136 || r.Change.InputDigest == "" {
		t.Fatalf("schema mapping dropped exclusion data: %+v", r.Change)
	}
}

// TestSuppressedMentionsAreDisclosed: when the premise gate drops spec-id
// mentions (no definition corpus), BOTH renderers state the suppression —
// removing remainder rows silently would be the exact dishonesty the
// remainder exists to prevent.
func TestSuppressedMentionsAreDisclosed(t *testing.T) {
	r := Assemble(gitdiff.Change{}, nil, nil, schema.Coverage{SuppressedMentions: 7})

	var md strings.Builder
	WriteMarkdown(&md, r)
	if !strings.Contains(md.String(), "7 spec-id mention(s) not minted") {
		t.Errorf("markdown receipt omits the suppression disclosure:\n%s", md.String())
	}

	var txt strings.Builder
	WriteText(&txt, r)
	if !strings.Contains(txt.String(), "7 spec-id mention(s) not minted") {
		t.Errorf("text receipt omits the suppression disclosure:\n%s", txt.String())
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

// TestRefutedDetailComesFromFailingProbe: a refuted claim renders the FAILING
// probe's detail, not an earlier passing probe's "ok".
func TestRefutedDetailComesFromFailingProbe(t *testing.T) {
	res := schema.ClaimResult{
		Claim:  schema.Claim{ID: "INV-1", Text: "holds"},
		Status: schema.StatusRefuted,
		Evidence: []schema.Evidence{
			{ClaimID: "INV-1", ProbeID: "p1", Ran: true, Passed: true, Detail: "ok ./pkg"},
			{ClaimID: "INV-1", ProbeID: "p2", Ran: true, Passed: false, Detail: "assertion failed: boom"},
		},
	}
	if got := detailOf(res); got != "assertion failed: boom" {
		t.Fatalf("detailOf = %q, want the failing probe's detail", got)
	}
}

// TestReceiptSanitizesPathsAndPinsSHAs: the receipt names the repo, never its
// location; probe details are scrubbed of the repo root; the header carries
// the immutable SHA pins.
func TestReceiptSanitizesPathsAndPinsSHAs(t *testing.T) {
	change := gitdiff.Change{
		Repo: "/home/someone/src/proj", BaseRef: "main", HeadRef: "feature",
		BaseSHA: "aaaabbbbccccdddd", HeadSHA: "1111222233334444",
	}
	claims := []schema.Claim{{ID: "A", Text: "t", ProbeIDs: []string{"p"}}}
	evidence := [][]schema.Evidence{{{ClaimID: "A", ProbeID: "p", Ran: true, Passed: false,
		Detail: "FAIL /home/someone/src/proj/pkg/x_test.go:12"}}}
	r := Assemble(change, claims, evidence, schema.Coverage{})

	if r.Change.Repo != "proj" {
		t.Errorf("receipt repo = %q, want the basename only", r.Change.Repo)
	}
	if d := r.Results[0].Evidence[0].Detail; strings.Contains(d, "/home/someone") {
		t.Errorf("detail leaks the repo location: %q", d)
	}
	if r.Change.BaseSHA == "" || r.Change.HeadSHA == "" {
		t.Errorf("SHAs missing: %+v", r.Change)
	}
	var text strings.Builder
	WriteText(&text, r)
	if !strings.Contains(text.String(), "(aaaabbbbcccc..111122223333)") {
		t.Errorf("text header missing SHA pins:\n%s", text.String())
	}
}

// TestDetailSanitizationIsCategorical: the sanitizer is a categorical rule at
// the Assemble chokepoint, not an enumerated blocklist — EVERY absolute path
// outside the repo collapses to its basename, closing the classes a
// root+home enumeration left open: temp paths, toolchain roots, sibling
// project names, other users' homes. In-repo paths stay fully readable, and
// file:line actionability survives the collapse.
func TestDetailSanitizationIsCategorical(t *testing.T) {
	const repo = "/home/someone/src/proj"
	cases := []struct{ in, want string }{
		// In-repo: full relative path preserved (the reader's business).
		{repo + "/pkg/x_test.go:12: boom", "./pkg/x_test.go:12: boom"},
		// Temp path.
		{"wrote /tmp/probe-x1/out.json", "wrote …/out.json"},
		// Sibling project beside the repo: the name must not survive.
		{"read /home/someone/src/otherproj/secret.cs:3", "read …/secret.cs:3"},
		// Toolchain root, file:line intact.
		{"panic at /usr/lib/go/src/testing/testing.go:1576: died", "panic at …/testing.go:1576: died"},
		// Flag-glued path.
		{"-coverprofile=/tmp/cov1/prof.out", "-coverprofile=…/prof.out"},
		// URLs are not filesystem paths and pass untouched.
		{"GET http://example.com/a/b: refused", "GET http://example.com/a/b: refused"},
		// Repo-relative output was never a leak.
		{"internal/probe/gotest.go:41: ok", "internal/probe/gotest.go:41: ok"},
		// Single-component absolute names identify nothing.
		{"read /dev: is a directory", "read /dev: is a directory"},
	}
	for _, c := range cases {
		if got := sanitizePaths(c.in, repo); got != c.want {
			t.Errorf("sanitizePaths(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}

	if got := scrubHost("dial tcp devbox42:8080: refused", "devbox42"); got != "dial tcp <host>:8080: refused" {
		t.Errorf("hostname survived: %q", got)
	}
	if got := scrubHost("go test ok", "go"); got != "go test ok" {
		t.Errorf("short-hostname guard failed: %q", got)
	}
}
