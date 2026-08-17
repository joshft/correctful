package probe

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshft/correctful/schema"
)

// realProfileSnippet is shaped verbatim after a measured `go test
// -coverprofile -coverpkg=./...` output (captured 2026-08-17, module path
// anonymized): mode header, col-qualified spans, executed and unexecuted
// blocks side by side.
const realProfileSnippet = `mode: set
example.com/mod/cmd/tool/cmd_config.go:18.65,19.20 1 1
example.com/mod/cmd/tool/cmd_config.go:19.20,22.3 2 0
example.com/mod/cmd/tool/cmd_config.go:24.2,24.17 1 1
example.com/mod/pkg/other/other.go:5.10,7.2 1 0
`

func TestParseCoverProfileRealShape(t *testing.T) {
	prof := parseCoverProfile(realProfileSnippet)
	blocks := prof["example.com/mod/cmd/tool/cmd_config.go"]
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3: %+v", len(blocks), blocks)
	}
	if blocks[0] != (covBlock{startLine: 18, endLine: 19, count: 1}) {
		t.Errorf("block 0 = %+v", blocks[0])
	}
	if blocks[1].count != 0 {
		t.Errorf("block 1 count = %d, want 0", blocks[1].count)
	}
	if got := profileBlocksFor(prof, "cmd/tool/cmd_config.go"); len(got) != 3 {
		t.Errorf("suffix match failed: %+v", got)
	}
	if got := profileBlocksFor(prof, "tool/cmd_config.go"); got == nil {
		t.Errorf("component-boundary suffix should match")
	}
	if got := profileBlocksFor(prof, "config.go"); got != nil {
		t.Errorf("non-boundary suffix matched: %+v", got)
	}
}

// writeCovModule creates a real throwaway Go module: an annotated function,
// an unrelated function, a test that EXECUTES the annotated function, and a
// sibling test (named for the same id) that never calls it.
func writeCovModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/covmod\n\ngo 1.22\n",
		"gate.go": `package covmod

// INV-900: the gate rejects nil input.
func Gate(x []byte) bool {
	return x != nil
}

func Unrelated() int { return 1 }

// INV-901 lives on a const, outside any function: not checkable.
const RefOnConst = "INV-901"
`,
		"other.go": `package covmod

func Other() int { return 2 }
`,
		"gate_test.go": `package covmod

import "testing"

func TestINV900_GateRejectsNil(t *testing.T) {
	if Gate(nil) {
		t.Fatal("nil accepted")
	}
}

func TestINV900_NameOnlyNeverCallsGate(t *testing.T) {
	if Unrelated() != 1 {
		t.Fatal("unrelated broke")
	}
}

func TestOnlyOther(t *testing.T) {
	if Other() != 2 {
		t.Fatal("other broke")
	}
}
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestCoverageProvenBinding: end-to-end through the dispatcher against a real
// module. The claim carries a reference site (the annotation above Gate) and
// two probes named for it: the one that executes Gate earns binding
// "covered"; the one that never reaches it is disclosed "name-only". The
// VERDICT is identical for both — binding is orthogonal to pass/fail — and a
// second claim whose only site sits on a const gets no binding statement.
func TestCoverageProvenBinding(t *testing.T) {
	dir := writeCovModule(t)
	refSite := schema.Source{Kind: schema.SourceSpecID, File: "gate.go", Line: 3, Ref: "INV-900"}
	constSite := schema.Source{Kind: schema.SourceSpecID, File: "gate.go", Line: 10, Ref: "INV-901"}
	pCovered := schema.GoTestProbeID("gate_test.go", "TestINV900_GateRejectsNil")
	pNameOnly := schema.GoTestProbeID("gate_test.go", "TestINV900_NameOnlyNeverCallsGate")

	claims := []schema.Claim{
		{ID: "INV-900", Shape: schema.ShapeInvariant, Text: "gate rejects nil",
			ProbeIDs: []string{pCovered, pNameOnly}, RefSites: []schema.Source{refSite}},
		{ID: "INV-901", Shape: schema.ShapeInvariant, Text: "const-annotated",
			ProbeIDs: []string{pCovered}, RefSites: []schema.Source{constSite}},
	}
	evidence := NewDispatcher(2, GoTestRunner{}).Dispatch(context.Background(), dir, claims)

	if len(evidence) != 2 || len(evidence[0]) != 2 {
		t.Fatalf("evidence shape = %v", evidence)
	}
	covered, nameOnly := evidence[0][0], evidence[0][1]
	if !covered.Ran || !covered.Passed || !nameOnly.Ran || !nameOnly.Passed {
		t.Fatalf("verdicts degraded by instrumentation: %+v / %+v", covered, nameOnly)
	}
	if covered.Binding != "covered" {
		t.Errorf("executing probe binding = %q, want covered (detail %q)", covered.Binding, covered.Detail)
	}
	if nameOnly.Binding != "name-only" {
		t.Errorf("non-reaching probe binding = %q, want name-only", nameOnly.Binding)
	}
	if b := evidence[1][0].Binding; b != "" {
		t.Errorf("const-site claim binding = %q, want none (annotation not in any function)", b)
	}
}

// TestFileBindingFor: the file-level evaluator for model-proposed edges.
// Executed blocks anywhere in the file confirm the edge; a file whose blocks
// all read zero — or one the profile does not know — refutes it.
func TestFileBindingFor(t *testing.T) {
	prof := parseCoverProfile(realProfileSnippet)
	if got := fileBindingFor(prof, "cmd/tool/cmd_config.go"); got != schema.BindingFileCovered {
		t.Errorf("executed file = %q, want %q", got, schema.BindingFileCovered)
	}
	if got := fileBindingFor(prof, "pkg/other/other.go"); got != schema.BindingFileNotReached {
		t.Errorf("zero-count file = %q, want %q", got, schema.BindingFileNotReached)
	}
	if got := fileBindingFor(prof, "pkg/unknown/nope.go"); got != schema.BindingFileNotReached {
		t.Errorf("unknown file = %q, want %q", got, schema.BindingFileNotReached)
	}
}

// TestLLMEdgeFileBinding: end-to-end through the dispatcher. An LLM-proposed
// claim has no reference sites, yet its probe run IS instrumented (the
// pre-pass marks SourceLLM claims) and the edge is evaluated at file
// granularity against the claim's own file: the test that executes gate.go
// confirms the edge; the test that only touches other.go refutes it. The
// VERDICTS stay untouched — both probes pass; only the edge differs.
func TestLLMEdgeFileBinding(t *testing.T) {
	dir := writeCovModule(t)
	pReaches := schema.GoTestProbeID("gate_test.go", "TestINV900_GateRejectsNil")
	pElsewhere := schema.GoTestProbeID("gate_test.go", "TestOnlyOther")

	claims := []schema.Claim{
		{ID: "LLM:gate.go:aaaa", Shape: schema.ShapeAssertion, Text: "gate rejects nil",
			Source:   schema.Source{Kind: schema.SourceLLM, File: "gate.go", Ref: "llm"},
			ProbeIDs: []string{pReaches}},
		{ID: "LLM:gate.go:bbbb", Shape: schema.ShapeAssertion, Text: "a claim its named test never touches",
			Source:   schema.Source{Kind: schema.SourceLLM, File: "gate.go", Ref: "llm"},
			ProbeIDs: []string{pElsewhere}},
	}
	evidence := NewDispatcher(2, GoTestRunner{}).Dispatch(context.Background(), dir, claims)

	if len(evidence) != 2 || len(evidence[0]) != 1 || len(evidence[1]) != 1 {
		t.Fatalf("evidence shape = %v", evidence)
	}
	confirmed, refutedEdge := evidence[0][0], evidence[1][0]
	if !confirmed.Ran || !confirmed.Passed || !refutedEdge.Ran || !refutedEdge.Passed {
		t.Fatalf("verdicts degraded by instrumentation: %+v / %+v", confirmed, refutedEdge)
	}
	if confirmed.Binding != schema.BindingFileCovered {
		t.Errorf("edge into executed file = %q, want %q (detail %q)",
			confirmed.Binding, schema.BindingFileCovered, confirmed.Detail)
	}
	if refutedEdge.Binding != schema.BindingFileNotReached {
		t.Errorf("edge into untouched file = %q, want %q",
			refutedEdge.Binding, schema.BindingFileNotReached)
	}
}
