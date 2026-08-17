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

// writePairModule creates a real throwaway module with the test shapes the
// compound pair verdict must distinguish.
func writePairModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/pairs\n\ngo 1.22\n",
		"p_test.go": `package pairs

import "testing"

func TestAcceptsGood(t *testing.T) {}
func TestRejectsBad(t *testing.T)  {}
func TestRejectFails(t *testing.T) { t.Fatal("negative case was accepted: bad") }
func TestAcceptSkips(t *testing.T) { t.Skip("env missing") }
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestGoTestPairVerdictsFromEventStream: the compound verdict comes from the
// -json event stream under the single runner's discipline. The pair passes
// only on an explicit pass event for EACH side; a failing side refutes with
// that side's own output; a skipped side, a missing side, and a build
// failure are all Ran=false — every one of them exits in a way exit-code
// trust would misread (skip and single-match exit 0; an infra failure exits
// non-zero, which the old text path recorded as a REFUTATION).
func TestGoTestPairVerdictsFromEventStream(t *testing.T) {
	dir := writePairModule(t)
	run := func(accept, reject string) schema.Evidence {
		t.Helper()
		pid := schema.GoTestPairProbeID(".", accept, reject)
		return GoTestPairRunner{}.Run(context.Background(), dir,
			schema.Claim{ID: "P"}, pid)
	}

	if ev := run("TestAcceptsGood", "TestRejectsBad"); !ev.Ran || !ev.Passed ||
		ev.Tier != schema.T2Adversarial || !strings.Contains(ev.Detail, "pair ok") {
		t.Errorf("pass/pass: %+v", ev)
	}
	if ev := run("TestAcceptsGood", "TestRejectFails"); !ev.Ran || ev.Passed ||
		!strings.Contains(ev.Detail, "reject side failed") || !strings.Contains(ev.Detail, "negative case was accepted") {
		t.Errorf("fail side: %+v, want refutation carrying the failing side's own output", ev)
	}
	if ev := run("TestAcceptSkips", "TestRejectsBad"); ev.Ran || ev.Passed ||
		!strings.Contains(ev.Detail, "skip") {
		t.Errorf("skip side: %+v — a skipped side exits 0 and must NOT verify a pair", ev)
	}
	if ev := run("TestAcceptsGood", "TestRenamedAway"); ev.Ran || ev.Passed ||
		!strings.Contains(ev.Detail, "TestRenamedAway did not run") {
		t.Errorf("missing side: %+v — a single-match run exits 0 and must NOT verify a pair", ev)
	}
}

// TestGoTestPairBuildFailureIsNotARefutation: an infra failure (the package
// does not compile) exits non-zero; the old text-output path recorded that
// as Ran=true/Passed=false — a false refutation that would block a merge
// over a broken fixture. The event stream has no terminal test events, so
// the verdict is not-run.
func TestGoTestPairBuildFailureIsNotARefutation(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/broken\n\ngo 1.22\n",
		"b_test.go": "package broken\n\nfunc TestA(t *testing.T) {} // missing testing import: does not compile\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pid := schema.GoTestPairProbeID(".", "TestA", "TestB")
	ev := GoTestPairRunner{}.Run(context.Background(), dir, schema.Claim{ID: "P"}, pid)
	if ev.Ran || ev.Passed {
		t.Errorf("build failure: %+v — nothing executed, so nothing is refuted", ev)
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
