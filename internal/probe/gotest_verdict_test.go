package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

// writeVerdictModule creates a real throwaway module with one test per
// verdict shape: pass, fail, and — the measured false-pass hazard — skip,
// which exits 0.
func writeVerdictModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/verdicts\n\ngo 1.22\n",
		"v_test.go": `package verdicts

import "testing"

func TestPasses(t *testing.T)  {}
func TestFails(t *testing.T)   { t.Fatal("assertion failed: boom") }
func TestSkipped(t *testing.T) { t.Skip("not on this platform") }
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestGoTestVerdictsFromEventStream: the verdict comes from the -json event
// stream, never the exit status. A pass event verifies; a fail event refutes
// with the failing test's own output as detail; a SKIP — which exits 0 and
// would confer a false T1 under exit-code trust — is Ran=false, as is a
// pattern that matches nothing.
func TestGoTestVerdictsFromEventStream(t *testing.T) {
	dir := writeVerdictModule(t)
	run := func(name string) schema.Evidence {
		t.Helper()
		pid := schema.GoTestProbeID("v_test.go", name)
		return GoTestRunner{}.Run(context.Background(), dir,
			schema.Claim{ID: "C-" + name}, pid)
	}

	if ev := run("TestPasses"); !ev.Ran || !ev.Passed {
		t.Errorf("pass: %+v", ev)
	}
	if ev := run("TestFails"); !ev.Ran || ev.Passed || !strings.Contains(ev.Detail, "boom") {
		t.Errorf("fail: %+v, want refutation carrying the test's own failure output", ev)
	}
	if ev := run("TestSkipped"); ev.Ran || ev.Passed || !strings.Contains(ev.Detail, "skip") {
		t.Errorf("skip: %+v — a skipped test exits 0 and must NOT verify", ev)
	}
	if ev := run("TestNoSuchTest"); ev.Ran || ev.Passed {
		t.Errorf("no-match: %+v, want not-run", ev)
	}
}

// overTierRunner claims more tier than it declares — the misbehavior the
// dispatcher must cap.
type overTierRunner struct{}

func (overTierRunner) CanRun(pid string) bool { return strings.HasPrefix(pid, "go-test:") }
func (overTierRunner) MaxTier() schema.Tier   { return schema.T1Assertion }
func (overTierRunner) Run(_ context.Context, _ string, c schema.Claim, pid string) schema.Evidence {
	return schema.Evidence{ClaimID: c.ID, ProbeID: pid, Tier: schema.T4Mechanical, Ran: true, Passed: true}
}

// TestDispatcherCapsTierAtRunnerMax: tier-as-probe-property is load-bearing,
// so the dispatcher enforces it — evidence can never exceed the runner's
// declared maximum, whatever the runner wrote.
func TestDispatcherCapsTierAtRunnerMax(t *testing.T) {
	claims := []schema.Claim{{ID: "X", ProbeIDs: []string{"go-test:a_test.go:TestX"}}}
	evidence := NewDispatcher(1, overTierRunner{}).Dispatch(context.Background(), t.TempDir(), claims)
	if got := evidence[0][0].Tier; got != schema.T1Assertion {
		t.Fatalf("tier = %v, want capped at the runner's declared T1", got)
	}
}
