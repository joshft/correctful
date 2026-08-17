package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

// TestEvidenceCarriesMechanismAndEnvironment: every runner states its own
// mechanism on the evidence it returns — including early returns, so a
// policy engine never has to parse probe ids — and the go runners measure
// the toolchain the probe actually ran under. Scope stays EMPTY on an
// uninstrumented run: unmeasured is stated as unmeasured.
func TestEvidenceCarriesMechanismAndEnvironment(t *testing.T) {
	dir := writeCovModule(t)
	pid := schema.GoTestProbeID("gate_test.go", "TestINV900_GateRejectsNil")
	ev := GoTestRunner{}.Run(context.Background(), dir, schema.Claim{ID: "C"}, pid)
	if ev.Mechanism != schema.MechanismGoTest {
		t.Errorf("go-test mechanism = %q", ev.Mechanism)
	}
	if !strings.HasPrefix(ev.Environment, "go1") || !strings.Contains(ev.Environment, "/") {
		t.Errorf("go environment = %q, want measured \"go1... os/arch\"", ev.Environment)
	}
	if ev.Scope != "" {
		t.Errorf("uninstrumented run scope = %q, want unmeasured (empty)", ev.Scope)
	}

	pairDir := writePairModule(t)
	pairID := schema.GoTestPairProbeID(".", "TestAcceptsGood", "TestRejectsBad")
	if ev := (GoTestPairRunner{}).Run(context.Background(), pairDir, schema.Claim{ID: "P"}, pairID); ev.Mechanism != schema.MechanismGoTestPair || ev.Environment == "" {
		t.Errorf("pair evidence class = %q / %q", ev.Mechanism, ev.Environment)
	}

	// Early returns keep the mechanism: malformed ids never execute anything,
	// yet the evidence still states which runner refused.
	if ev := (GoTestRunner{}).Run(context.Background(), dir, schema.Claim{ID: "C"}, "go-test:nofile"); ev.Mechanism != schema.MechanismGoTest {
		t.Errorf("malformed go-test mechanism = %q", ev.Mechanism)
	}
	if ev := (DotnetTestRunner{}).Run(context.Background(), dir, schema.Claim{ID: "C"}, "dotnet-test:x"); ev.Mechanism != schema.MechanismDotnetTest {
		t.Errorf("malformed dotnet mechanism = %q", ev.Mechanism)
	}
	if ev := (AlloyCheckRunner{}).Run(context.Background(), dir, schema.Claim{ID: "C"}, "alloy-check:x"); ev.Mechanism != schema.MechanismAlloyCheck {
		t.Errorf("malformed alloy mechanism = %q", ev.Mechanism)
	}
}

// TestScopeOfProfile: the footprint reduction. Executed blocks in one
// directory are single-package; a second directory with an executed block
// makes it cross-package; a profile with no executed block stays unmeasured.
func TestScopeOfProfile(t *testing.T) {
	single := parseCoverProfile(realProfileSnippet) // executed blocks in cmd/tool only
	if got := scopeOf(single); got != schema.ScopeSinglePackage {
		t.Errorf("single = %q", got)
	}
	cross := parseCoverProfile(realProfileSnippet +
		"example.com/mod/pkg/other/other.go:9.1,11.2 1 3\n")
	if got := scopeOf(cross); got != schema.ScopeCrossPackage {
		t.Errorf("cross = %q", got)
	}
	unexecuted := parseCoverProfile("mode: set\nexample.com/mod/a/a.go:1.1,2.2 1 0\n")
	if got := scopeOf(unexecuted); got != "" {
		t.Errorf("unexecuted = %q, want unmeasured", got)
	}
}

// writeCrossPkgModule creates a real module where the test's execution spans
// two packages: the test (in a) calls through a into b.
func writeCrossPkgModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/xmod\n\ngo 1.22\n",
		"a/a.go": `package a

import "example.com/xmod/b"

func A() int { return b.B() + 1 }
`,
		"b/b.go": `package b

func B() int { return 41 }
`,
		"a/a_test.go": `package a

import "testing"

func TestAUsesB(t *testing.T) {
	if A() != 42 {
		t.Fatal("wrong")
	}
}
`,
	}
	for rel, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestScopeMeasuredThroughDispatcher: end-to-end — an instrumented run's
// scope lands on the evidence. The cross-package module's test executes a
// and b, so the LLM edge into b/b.go is BOTH confirmed (file-covered) and
// classified cross-package; the single-package module classifies
// single-package on the same path.
func TestScopeMeasuredThroughDispatcher(t *testing.T) {
	crossDir := writeCrossPkgModule(t)
	crossClaim := schema.Claim{ID: "LLM:b/b.go:cccc", Shape: schema.ShapeAssertion,
		Text:     "A adds one to B's answer",
		Source:   schema.Source{Kind: schema.SourceLLM, File: "b/b.go", Ref: "llm"},
		ProbeIDs: []string{schema.GoTestProbeID("a/a_test.go", "TestAUsesB")}}
	evidence := NewDispatcher(1, GoTestRunner{}).Dispatch(context.Background(), crossDir, []schema.Claim{crossClaim})
	ev := evidence[0][0]
	if !ev.Ran || !ev.Passed || ev.Binding != schema.BindingFileCovered {
		t.Fatalf("cross-module run degraded: %+v", ev)
	}
	if ev.Scope != schema.ScopeCrossPackage {
		t.Errorf("cross-package scope = %q, want %q", ev.Scope, schema.ScopeCrossPackage)
	}

	singleDir := writeCovModule(t)
	singleClaim := schema.Claim{ID: "LLM:gate.go:dddd", Shape: schema.ShapeAssertion,
		Text:     "gate rejects nil",
		Source:   schema.Source{Kind: schema.SourceLLM, File: "gate.go", Ref: "llm"},
		ProbeIDs: []string{schema.GoTestProbeID("gate_test.go", "TestINV900_GateRejectsNil")}}
	evidence = NewDispatcher(1, GoTestRunner{}).Dispatch(context.Background(), singleDir, []schema.Claim{singleClaim})
	if got := evidence[0][0].Scope; got != schema.ScopeSinglePackage {
		t.Errorf("single-package scope = %q, want %q", got, schema.ScopeSinglePackage)
	}
}
