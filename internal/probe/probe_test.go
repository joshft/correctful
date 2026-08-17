package probe

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/joshft/correctful/schema"
)

// TestGoTestProbeIDRoundTrips: a go-test probe id parses back into the file and
// test name it was minted from, including file paths that contain slashes.
func TestGoTestProbeIDRoundTrips(t *testing.T) {
	id := schema.GoTestProbeID("internal/probe/probe_test.go", "TestGoTestProbeIDRoundTrips")
	file, name, ok := schema.ParseGoTestProbeID(id)
	if !ok {
		t.Fatal("parse failed")
	}
	if file != "internal/probe/probe_test.go" {
		t.Errorf("file = %q", file)
	}
	if name != "TestGoTestProbeIDRoundTrips" {
		t.Errorf("name = %q", name)
	}
}

// TestGoTestProbeIDRejectsMalformed: an id missing its parts is rejected, not
// silently run against an empty target.
func TestGoTestProbeIDRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"go-test:", "go-test:onlyfile", "go-test::TestX", "go-test:file:", "elsewhere:x:y"} {
		if _, _, ok := schema.ParseGoTestProbeID(bad); ok {
			t.Errorf("ParseGoTestProbeID(%q) accepted a malformed id", bad)
		}
	}
}

// TestPairProbeIDRoundTrips: a pair probe id parses back into its package dir
// and the accept/reject test names it was minted from.
func TestPairProbeIDRoundTrips(t *testing.T) {
	id := schema.GoTestPairProbeID("pkg/config", "TestAcceptsGood", "TestRejectsBad")
	dir, accept, reject, ok := schema.ParseGoTestPairProbeID(id)
	if !ok {
		t.Fatal("parse failed")
	}
	if dir != "pkg/config" || accept != "TestAcceptsGood" || reject != "TestRejectsBad" {
		t.Errorf("parsed dir=%q accept=%q reject=%q", dir, accept, reject)
	}
	for _, bad := range []string{"go-test-pair:", "go-test-pair:dir:OnlyOne", "go-test-pair:dir:|B", "go-test-pair:dir:A|"} {
		if _, _, _, ok := schema.ParseGoTestPairProbeID(bad); ok {
			t.Errorf("ParseGoTestPairProbeID(%q) accepted a malformed id", bad)
		}
	}
}

// TestGoTestRunnerConfersT1: the go-test runner confers exactly T1 — a checker
// must under-claim, never over-claim, the strength of its evidence.
func TestGoTestRunnerConfersT1(t *testing.T) {
	if (GoTestRunner{}).MaxTier() != schema.T1Assertion {
		t.Fatalf("go-test MaxTier = %v, want T1", (GoTestRunner{}).MaxTier())
	}
}

// TestGoTestPairRunnerConfersT2: a passing accept/reject pair is the
// adversarial shape, and only the pair runner may confer T2.
func TestGoTestPairRunnerConfersT2(t *testing.T) {
	if (GoTestPairRunner{}).MaxTier() != schema.T2Adversarial {
		t.Fatalf("pair MaxTier = %v, want T2", (GoTestPairRunner{}).MaxTier())
	}
	if (GoTestRunner{}).CanRun(schema.GoTestPairProbeID("d", "A", "B")) {
		t.Fatal("single-test runner claimed a pair probe id")
	}
	if !(GoTestPairRunner{}).CanRun(schema.GoTestPairProbeID("d", "A", "B")) {
		t.Fatal("pair runner rejected its own probe id")
	}
}

// TestDotnetProbeIDRoundTrips: a dotnet-test probe id parses back into the
// csproj path and Class.Method it was minted from.
func TestDotnetProbeIDRoundTrips(t *testing.T) {
	id := schema.DotnetTestProbeID("gate/Example.Gate.Tests/Example.Gate.Tests.csproj", "Inv018AcceptancePolicyTests.Pinned_policy_validates")
	csproj, cdm, ok := schema.ParseDotnetTestProbeID(id)
	if !ok || csproj != "gate/Example.Gate.Tests/Example.Gate.Tests.csproj" || cdm != "Inv018AcceptancePolicyTests.Pinned_policy_validates" {
		t.Fatalf("parsed csproj=%q cdm=%q ok=%v", csproj, cdm, ok)
	}
	for _, bad := range []string{"dotnet-test:", "dotnet-test:onlyproj", "dotnet-test::X.Y", "dotnet-test:p.csproj:"} {
		if _, _, ok := schema.ParseDotnetTestProbeID(bad); ok {
			t.Errorf("ParseDotnetTestProbeID(%q) accepted a malformed id", bad)
		}
	}
}

// TestInterpretDotnetOutput: verdicts come from summary COUNTS, never exit
// status. The pass and no-match cases are VERBATIM lines captured from a real
// `dotnet test` captured from a real run (2026-08-16; project names anonymized) — where, measured,
// a filter matching nothing still EXITS 0. Trusting the exit code would mint a
// pass for a phantom test.
func TestInterpretDotnetOutput(t *testing.T) {
	pass := "Test run for /x/Example.Gate.Tests.dll (.NETCoreApp,Version=v10.0)\n" +
		"A total of 1 test files matched the specified pattern.\n\n" +
		"Passed!  - Failed:     0, Passed:     1, Skipped:     0, Total:     1, Duration: 4 ms - Example.Gate.Tests.dll (net10.0)\n"
	noMatch := "Test run for /x/Example.Gate.Tests.dll (.NETCoreApp,Version=v10.0)\n" +
		"A total of 1 test files matched the specified pattern.\n" +
		"No test matches the given testcase filter `FullyQualifiedName~NoSuchTestNameXyz` in /x/Example.Gate.Tests.dll\n"
	fail := "Failed!  - Failed:     1, Passed:     6, Skipped:     0, Total:     7, Duration: 2 s - Example.Gate.Tests.dll (net10.0)\n"
	build := "CSC : error CS1002: ; expected\nBuild FAILED.\n"
	skipped := "Passed!  - Failed:     0, Passed:     0, Skipped:     1, Total:     1, Duration: 1 ms - X.dll (net10.0)\n"

	if ran, passed, _ := interpretDotnetOutput(pass, nil); !ran || !passed {
		t.Errorf("pass case: ran=%v passed=%v, want true/true", ran, passed)
	}
	// The critical case: no-match arrives with runErr == nil (exit 0).
	if ran, _, _ := interpretDotnetOutput(noMatch, nil); ran {
		t.Error("no-match with exit 0 was recorded as ran — phantom test minted evidence")
	}
	if ran, passed, _ := interpretDotnetOutput(fail, errFake); !ran || passed {
		t.Errorf("fail case: ran=%v passed=%v, want true/false (refuted)", ran, passed)
	}
	if ran, _, _ := interpretDotnetOutput(build, errFake); ran {
		t.Error("build failure was recorded as ran — it refutes nothing")
	}
	if ran, _, _ := interpretDotnetOutput(skipped, nil); ran {
		t.Error("all-skipped was recorded as ran")
	}
}

var errFake = errFakeType{}

type errFakeType struct{}

func (errFakeType) Error() string { return "exit status 1" }

// countingRunner records how many times each probe id executes.
type countingRunner struct {
	mu    sync.Mutex
	calls map[string]int
}

func (r *countingRunner) CanRun(id string) bool { return strings.HasPrefix(id, "count:") }
func (r *countingRunner) MaxTier() schema.Tier  { return schema.T1Assertion }
func (r *countingRunner) Run(_ context.Context, _ string, claim schema.Claim, pid string) schema.Evidence {
	r.mu.Lock()
	r.calls[pid]++
	r.mu.Unlock()
	return schema.Evidence{ClaimID: claim.ID, ProbeID: pid, Tier: schema.T1Assertion, Ran: true, Passed: true}
}

// TestDispatchRunsSharedProbeOnce: a probe id bound under several claims (one
// test method of a class named for two invariants) executes exactly once, and
// its single verdict is attributed to every binding claim with the right
// ClaimID.
func TestDispatchRunsSharedProbeOnce(t *testing.T) {
	r := &countingRunner{calls: map[string]int{}}
	claims := []schema.Claim{
		{ID: "INV-010", ProbeIDs: []string{"count:shared", "count:only10"}},
		{ID: "INV-011", ProbeIDs: []string{"count:shared"}},
	}
	out := NewDispatcher(4, r).Dispatch(context.Background(), ".", claims)

	if r.calls["count:shared"] != 1 {
		t.Errorf("shared probe ran %d times, want 1", r.calls["count:shared"])
	}
	if r.calls["count:only10"] != 1 {
		t.Errorf("unshared probe ran %d times, want 1", r.calls["count:only10"])
	}
	if out[0][0].ClaimID != "INV-010" || out[1][0].ClaimID != "INV-011" {
		t.Errorf("claim attribution wrong: %q / %q", out[0][0].ClaimID, out[1][0].ClaimID)
	}
	if !out[1][0].Verified() {
		t.Error("shared probe's verdict not attributed to the second claim")
	}
}

// TestAlloyVerdictSemantics: a check passes when NO solution exists (no
// counterexample in scope) and a run witness passes when a solution EXISTS
// (the model is not vacuous) — inverted semantics that must never be conflated,
// or a vacuous model would read as sixteen passing safety checks. The zero-
// solutions check case mirrors the real Alloy receipt, where a passing check
// carries NO "solution" key at all.
func TestAlloyVerdictSemantics(t *testing.T) {
	if ran, passed, _ := alloyVerdict(alloyCmd{Type: "check", Solutions: 0}); !ran || !passed {
		t.Errorf("clean check: ran=%v passed=%v, want true/true", ran, passed)
	}
	if ran, passed, _ := alloyVerdict(alloyCmd{Type: "check", Solutions: 1}); !ran || passed {
		t.Errorf("counterexample: ran=%v passed=%v, want true/false (refuted)", ran, passed)
	}
	if ran, passed, _ := alloyVerdict(alloyCmd{Type: "run", Solutions: 1}); !ran || !passed {
		t.Errorf("witness found: ran=%v passed=%v, want true/true", ran, passed)
	}
	if ran, passed, _ := alloyVerdict(alloyCmd{Type: "run", Solutions: 0}); !ran || passed {
		t.Errorf("vacuous run: ran=%v passed=%v, want true/false (refuted)", ran, passed)
	}
	if ran, _, _ := alloyVerdict(alloyCmd{Type: "mystery"}); ran {
		t.Error("unknown command type recorded as ran")
	}
}

// TestAlloyReceiptParsing: against a VERBATIM fragment of a real Alloy 6.2.0
// JSON receipt (a real Alloy 6.2.0 run, 2026-08-16; names anonymized) — where a passing check
// has NO "solution" key (absence must read as zero solutions) and the run
// witness lists its solution files.
func TestAlloyReceiptParsing(t *testing.T) {
	raw := `{"commands":{` +
		`"GuardNeverWidensAccess":{"bitwidth":4,"expects":-1,"name":"GuardNeverWidensAccess",` +
		`"scopes":["exactly 12 Snapshot","3 Task"],"type":"check"},` +
		`"ExerciseProtocol":{"type":"run","solution":["ExerciseProtocol-solution-0.json"]}}}`
	var receipt struct {
		Commands map[string]struct {
			Type     string            `json:"type"`
			Solution []json.RawMessage `json:"solution"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatal(err)
	}
	check := receipt.Commands["GuardNeverWidensAccess"]
	if check.Type != "check" || len(check.Solution) != 0 {
		t.Errorf("check parsed as type=%q solutions=%d, want check/0", check.Type, len(check.Solution))
	}
	witness := receipt.Commands["ExerciseProtocol"]
	if witness.Type != "run" || len(witness.Solution) != 1 {
		t.Errorf("run parsed as type=%q solutions=%d, want run/1", witness.Type, len(witness.Solution))
	}
}

// TestAlloyProbeIDRoundTrips and rejects malformed ids.
func TestAlloyProbeIDRoundTrips(t *testing.T) {
	id := schema.AlloyCheckProbeID("formal/lifecycle.als", "StatesNeverAlias")
	file, command, ok := schema.ParseAlloyCheckProbeID(id)
	if !ok || file != "formal/lifecycle.als" || command != "StatesNeverAlias" {
		t.Fatalf("parsed file=%q command=%q ok=%v", file, command, ok)
	}
	for _, bad := range []string{"alloy-check:", "alloy-check:onlyfile", "alloy-check::X", "alloy-check:f.als:"} {
		if _, _, ok := schema.ParseAlloyCheckProbeID(bad); ok {
			t.Errorf("ParseAlloyCheckProbeID(%q) accepted a malformed id", bad)
		}
	}
	if (AlloyCheckRunner{}).MaxTier() != schema.T3Property {
		t.Errorf("alloy MaxTier = %v, want T3", (AlloyCheckRunner{}).MaxTier())
	}
}

// TestCouldNotRunDetectsBuildFailure: a build failure is "did not run", so it
// can never be recorded as a refutation.
func TestCouldNotRunDetectsBuildFailure(t *testing.T) {
	if !couldNotRun("FAIL\t[build failed]") {
		t.Error("build failure not detected as could-not-run")
	}
	if couldNotRun("--- FAIL: TestX (0.00s)") {
		t.Error("a real test failure was misread as could-not-run")
	}
}

// TestPassedInExcludesSubtestsAndPrefixes: the pass check matches exactly the
// named top-level test — not its subtests, not a longer name sharing the
// prefix. A sloppy match here would let a pair pass on evidence from the wrong
// test.
func TestPassedInExcludesSubtestsAndPrefixes(t *testing.T) {
	out := "=== RUN   TestA\n--- PASS: TestA/sub (0.00s)\n--- PASS: TestABC (0.01s)\n"
	if testPassedIn(out, "TestA") {
		t.Error("subtest or prefix collision counted as a top-level pass")
	}
	if !testPassedIn("--- PASS: TestA (0.02s)\n", "TestA") {
		t.Error("a genuine top-level pass was not recognized")
	}
}
