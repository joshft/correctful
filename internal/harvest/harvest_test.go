package harvest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joshft/correctful/schema"
)

// TestINV005_SegmentDetectionRecognizesMidNameIDs: a spec id embedded after a
// cluster prefix (a real-world convention) is recognized — a leading word
// boundary would miss it, misfiling the test as a plain assertion and dropping
// its invariant into the remainder as a false negative.
func TestINV005_SegmentDetectionRecognizesMidNameIDs(t *testing.T) {
	cases := map[string][]string{
		"ClusterC_INV004b_CheckRoutesThroughLoader": {"INV-004b"},
		"INV009_ZeroValueRejected":                  {"INV-009"},
		"INV001_And_BND002_Coupled":                 {"INV-001", "BND-002"},
		"F4_CheckSymlinkProtection_Enabled":         nil,
		"CheckRoutesThroughLoader":                  nil,
	}
	for name, want := range cases {
		got := specIDsInName(name)
		if len(got) != len(want) {
			t.Errorf("specIDsInName(%q) = %v, want %v", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("specIDsInName(%q)[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

// TestExtraBoundIDsBindsMultiIDNames: a test whose name encodes more than one id
// binds all of them (a coupled-invariant test), beyond the primary.
func TestExtraBoundIDsBindsMultiIDNames(t *testing.T) {
	got := extraBoundIDs("TestINV001_And_BND002_Coupled", "INV-001")
	if len(got) != 1 || got[0] != "BND-002" {
		t.Fatalf("extraBoundIDs = %v, want [BND-002]", got)
	}
}

// TestINV006_HarvestDoesNotBindDocCommentOnlyIDs: an id merely MENTIONED in a
// test's doc comment (a cross-reference, not a coverage claim) must not be bound
// as verified. Precision guard for the dogfood finding where doc-comment
// binding produced false passes (AP-004, INV-010, PAT-015). Only the test name
// is a coverage signal.
func TestINV006_HarvestDoesNotBindDocCommentOnlyIDs(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n" +
		"// TestFoo covers the loader. Unlike AP-999 it works; see INV-888.\n" +
		"func TestFoo(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	harvested, err := GoTestHarvester{}.Harvest(dir, []string{"x_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(harvested.Claims) == 0 {
		t.Fatal("expected TestFoo to be harvested as a claim")
	}
	for _, c := range harvested.Claims {
		if c.ID == "AP-999" || c.ID == "INV-888" {
			t.Errorf("doc-comment-only id %q was bound as a claim (precision violation)", c.ID)
		}
	}
}

// TestINV009_CoverageThreeWaySplit: the receipt's coverage disclosure splits
// changed files into claimed / scanned / unread — and a file whose claims
// MERGED into another file's claim still counts as claimed, because
// contribution is counted pre-merge. Undercounting a merge contributor would
// misreport a claim-bearing file as inert.
func TestINV009_CoverageThreeWaySplit(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a_test.go": "package x\nimport \"testing\"\nfunc TestINV900_HoldsHere(t *testing.T) {}\n",
		"b_test.go": "package x\nimport \"testing\"\nfunc TestINV900_HoldsThereToo(t *testing.T) {}\n",
		"plain.go":  "package x\nfunc helper() {}\n",
		"notes.md":  "prose about INV-901, which is not code\n",
		"data.bin":  "\x00\x01binary payload no harvester reads\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list := []string{"a_test.go", "b_test.go", "plain.go", "notes.md", "data.bin"}
	claims, cov, err := Run(dir, list, Default()...)
	if err != nil {
		t.Fatal(err)
	}

	// The two same-id tests merged into one claim carrying both probes.
	if len(claims) != 1 || claims[0].ID != "INV-900" || len(claims[0].ProbeIDs) != 2 {
		t.Fatalf("claims = %v, want one INV-900 with 2 probes", claims)
	}

	// notes.md counts as SCANNED, not unread: the rfc-must harvester opens
	// every candidate document to sniff for normative markers, and the sniff
	// is honestly a scan (it found none — the file yields zero claims).
	if cov.Claimed != 2 || cov.Scanned != 2 || cov.Unread != 1 {
		t.Fatalf("coverage split = claimed %d / scanned %d / unread %d, want 2/2/1",
			cov.Claimed, cov.Scanned, cov.Unread)
	}
	byFile := map[string]schema.FileCoverage{}
	for _, f := range cov.Files {
		byFile[f.File] = f
	}
	if byFile["b_test.go"].Claims == 0 {
		t.Error("merge contributor b_test.go reported as sourcing no claims (pre-merge counting broken)")
	}
	if len(byFile["plain.go"].ReadBy) == 0 || byFile["plain.go"].Claims != 0 {
		t.Errorf("plain.go = %+v, want read with zero claims (scanned)", byFile["plain.go"])
	}
	if len(byFile["notes.md"].ReadBy) == 0 || byFile["notes.md"].Claims != 0 {
		t.Errorf("notes.md = %+v, want sniffed by rfc-must with zero claims (its INV-901 mention is prose, not a claim)", byFile["notes.md"])
	}
	if len(byFile["data.bin"].ReadBy) != 0 {
		t.Errorf("data.bin read by %v, want unread", byFile["data.bin"].ReadBy)
	}
}

// TestINV003_SpecIDNormalizesToCanonicalForm: harvested identifiers in any
// surface form collapse to one canonical id, so a test and a spec reference to
// the same invariant reconcile to a single claim.
func TestINV003_SpecIDNormalizesToCanonicalForm(t *testing.T) {
	cases := map[string]string{
		"INV009":  "INV-009",
		"PRH_004": "PRH-004",
		"INV-012": "INV-012",
		"TB001":   "TB-001",
		"notanid": "",
		"INV9":    "", // fewer than 2 digits is not an id
	}
	for in, want := range cases {
		if got := normalizeSpecID(in); got != want {
			t.Errorf("normalizeSpecID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHumanizeSplitsCamelCase: a test name becomes a readable claim statement.
func TestHumanizeSplitsCamelCase(t *testing.T) {
	if got := humanize("ZeroValuePolicyRejected"); got != "Zero Value Policy Rejected" {
		t.Fatalf("humanize = %q", got)
	}
	if got := humanize("Fail_Closed_OnEmpty"); got != "Fail Closed On Empty" {
		t.Fatalf("humanize snake = %q", got)
	}
}

// TestClaimFromInvariantTestNameBindsProbe: a test named for an invariant yields
// an invariant-shaped claim with the test bound as its probe.
func TestClaimFromInvariantTestNameBindsProbe(t *testing.T) {
	c := claimFromTestName("TestINV009_ZeroValueRejected", "pkg/x_test.go", 3)
	if c.ID != "INV-009" {
		t.Errorf("id = %q, want INV-009", c.ID)
	}
	if c.Shape != schema.ShapeInvariant {
		t.Errorf("shape = %q, want invariant", c.Shape)
	}
	if len(c.ProbeIDs) != 1 || c.ProbeIDs[0] != "go-test:pkg/x_test.go:TestINV009_ZeroValueRejected" {
		t.Errorf("probe ids = %v", c.ProbeIDs)
	}
}

// TestINV004_SpecRefHarvestsCodeNotProse: spec identifiers are claims only when
// they appear in shipped code. A catalog file that lists identifiers (an index,
// an architecture doc) is documentation about claims, not claims — scanning it
// floods the remainder with entries the change never asserted. Regression guard
// for the first external dogfood receipt (84 of 88 remainder claims
// were identifiers scraped from ARCHITECTURE.md / AGENT_CONTEXT.md).
func TestINV004_SpecRefHarvestsCodeNotProse(t *testing.T) {
	code := []string{"pkg/server.go", "cmd/main.c", "src/loader.cs", "x.ts"}
	prose := []string{"ARCHITECTURE.md", ".correctless/AGENT_CONTEXT.md", "docs/dev-journal.md", "notes.txt"}
	for _, f := range code {
		if !isCodeFile(f) {
			t.Errorf("isCodeFile(%q) = false, want true", f)
		}
	}
	for _, f := range prose {
		if isCodeFile(f) {
			t.Errorf("isCodeFile(%q) = true, want false", f)
		}
	}
	// Installed tooling under hidden directories is not the project's code:
	// its identifiers are the TOOLING's claims. Found on a real sweep where all
	// 75 remainder entries were dot-dir tooling scripts, zero project code.
	tooling := []string{".correctless/hooks/guard.sh", ".claude/x.sh", "a/.hidden/b.go", ".config.sh"}
	shipped := []string{"scripts/build.sh", "pkg/a/b.go"}
	for _, f := range tooling {
		if !underDotDir(f) {
			t.Errorf("underDotDir(%q) = false, want true", f)
		}
	}
	for _, f := range shipped {
		if underDotDir(f) {
			t.Errorf("underDotDir(%q) = true, want false", f)
		}
	}
}

// TestINV008_PairDetectionMintsT2PairProbe: an accept-polarity and a
// reject-polarity test bound to the same claim in the same package collapse
// into one compound pair probe; other bound tests are kept as individual
// probes so every test still runs.
func TestINV008_PairDetectionMintsT2PairProbe(t *testing.T) {
	claims := []schema.Claim{{
		ID: "INV-100", Shape: schema.ShapeInvariant,
		ProbeIDs: []string{
			schema.GoTestProbeID("pkg/x_test.go", "TestINV100_StrictRejectsLoosePerm"),
			schema.GoTestProbeID("pkg/x_test.go", "TestINV100_NoStrictAcceptsLoosePerm"),
			schema.GoTestProbeID("pkg/x_test.go", "TestINV100_ShowStatesStance"),
		},
	}}
	got := DetectPairs(claims)[0].ProbeIDs
	if len(got) != 2 {
		t.Fatalf("probe ids = %v, want [pair, individual]", got)
	}
	dir, accept, reject, ok := schema.ParseGoTestPairProbeID(got[0])
	if !ok {
		t.Fatalf("first probe %q is not a pair probe", got[0])
	}
	if dir != "pkg" || accept != "TestINV100_NoStrictAcceptsLoosePerm" || reject != "TestINV100_StrictRejectsLoosePerm" {
		t.Errorf("pair = dir %q accept %q reject %q", dir, accept, reject)
	}
	if got[1] != schema.GoTestProbeID("pkg/x_test.go", "TestINV100_ShowStatesStance") {
		t.Errorf("unknown-polarity probe not kept: %v", got)
	}
}

// TestPairDetectionRequiresSamePackage: an accept and a reject test in
// different packages do not form a pair — one invocation cannot run both, and
// a synthesized pass would be evidence that was never produced.
func TestPairDetectionRequiresSamePackage(t *testing.T) {
	claims := []schema.Claim{{
		ID: "INV-101",
		ProbeIDs: []string{
			schema.GoTestProbeID("pkg/a/a_test.go", "TestINV101_RejectsBad"),
			schema.GoTestProbeID("pkg/b/b_test.go", "TestINV101_AcceptsGood"),
		},
	}}
	got := DetectPairs(claims)[0].ProbeIDs
	for _, pid := range got {
		if _, _, _, ok := schema.ParseGoTestPairProbeID(pid); ok {
			t.Fatalf("cross-package pair was minted: %v", got)
		}
	}
}

// TestPolarityClassificationIsConservative: polarity words are exact segment
// matches; ambiguous or outcome-flavored names classify as neither, because a
// mis-paired probe would mint an unearned T2.
func TestPolarityClassificationIsConservative(t *testing.T) {
	cases := map[string]int{
		"TestFoo_RejectsBadInput":          -1,
		"TestFoo_AcceptsGoodInput":         +1,
		"TestFoo_GenuinelyUnchangedValid":  +1,
		"TestFoo_RendersUnavailable":       0, // no polarity vocabulary
		"TestFoo_AcceptsValidRejectsOther": 0, // both polarities: ambiguous
		"TestFoo_FailsClosed":              0, // "Fails" is an outcome, not a polarity
	}
	for name, want := range cases {
		if got := testPolarity(name); got != want {
			t.Errorf("testPolarity(%q) = %d, want %d", name, got, want)
		}
	}
}

// TestIsTestFunc: only real test entry points are harvested.
func TestIsTestFunc(t *testing.T) {
	yes := []string{"TestFoo", "TestINV009_X", "Test_helper", "Test9"}
	no := []string{"Test", "TestMain", "testFoo", "BenchmarkX", "helper"}
	for _, n := range yes {
		if !isTestFunc(n) {
			t.Errorf("isTestFunc(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if isTestFunc(n) {
			t.Errorf("isTestFunc(%q) = true, want false", n)
		}
	}
}

// TestRefSitesCollectedAndMerged: every code sighting of an id becomes a
// reference site (not just the first), and the merge into a probed test claim
// unions them — the coverage prover checks probes against these sites, so
// dropping any would blind it.
func TestRefSitesCollectedAndMerged(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":      "package x\n// INV-950: the gate holds.\nfunc gate() {}\n",
		"b.go":      "package x\n// see INV-950 for the gate contract\nfunc other() {}\n",
		"x_test.go": "package x\nimport \"testing\"\nfunc TestINV950_Holds(t *testing.T) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claims, _, err := Run(dir, []string{"a.go", "b.go", "x_test.go"}, Default()...)
	if err != nil {
		t.Fatal(err)
	}
	var got *schema.Claim
	for i := range claims {
		if claims[i].ID == "INV-950" {
			got = &claims[i]
		}
	}
	if got == nil {
		t.Fatalf("no INV-950 claim in %v", claims)
	}
	if len(got.ProbeIDs) != 1 {
		t.Errorf("probes = %v, want the test probe", got.ProbeIDs)
	}
	if len(got.RefSites) != 2 || got.RefSites[0].File != "a.go" || got.RefSites[1].File != "b.go" {
		t.Errorf("ref sites = %+v, want both code sightings in file order", got.RefSites)
	}
	if got.RefSites[0].Line != 2 {
		t.Errorf("a.go site line = %d, want 2", got.RefSites[0].Line)
	}
}

// TestSpecRefSkipsTestFilesAndTestdata: an id in a test file or under
// testdata/ is binding material or fixture content, not a shipped-code claim.
// Measured on a self-sweep: scanning test files filled the remainder with
// fixture identifiers.
func TestSpecRefSkipsTestFilesAndTestdata(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"gate.go":            "package x\n// INV-960: shipped-code claim.\nfunc g() {}\n",
		"gate_test.go":       "package x\n// INV-961 fixture mention\n",
		"testdata/sample.go": "package y\n// INV-962 fixture content\n",
	}
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := SpecRefHarvester{}.Harvest(dir, []string{"gate.go", "gate_test.go", "testdata/sample.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Claims) != 1 || res.Claims[0].ID != "INV-960" {
		t.Fatalf("claims = %+v, want only the shipped-code INV-960", res.Claims)
	}
}
