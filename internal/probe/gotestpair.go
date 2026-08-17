package probe

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/joshft/correctful/schema"
)

// GoTestPairRunner executes an accept/reject test pair as one compound probe.
// A pass means the positive case held AND the negative case was rejected — the
// adversarial shape — so a pass confers T2.
//
// Both tests run in a single `go test -v` invocation, and the runner then
// verifies from the verbose output that EACH named test actually executed and
// passed. Exit status alone is not enough: `go test` exits 0 when a -run
// pattern matches only one of the two names (the other was renamed or
// deleted), and consuming that as a pair pass would confer T2 on evidence that
// was never produced — a gate running against unverified substrate.
type GoTestPairRunner struct{}

func (GoTestPairRunner) CanRun(probeID string) bool {
	return strings.HasPrefix(probeID, schema.GoTestPairProbePrefix)
}

func (GoTestPairRunner) MaxTier() schema.Tier { return schema.T2Adversarial }

func (GoTestPairRunner) Run(ctx context.Context, repoDir string, claim schema.Claim, probeID string) schema.Evidence {
	ev := schema.Evidence{ClaimID: claim.ID, ProbeID: probeID, Tier: schema.T2Adversarial}

	pkgDir, acceptName, rejectName, ok := schema.ParseGoTestPairProbeID(probeID)
	if !ok {
		ev.Detail = "malformed go-test-pair probe id"
		return ev
	}

	pattern := "^(" + regexp.QuoteMeta(acceptName) + "|" + regexp.QuoteMeta(rejectName) + ")$"
	start := time.Now()
	out, err := runGoTest(ctx, repoDir, "./"+pkgDir, pattern, true)
	ev.Duration = time.Since(start).Round(time.Millisecond).String()

	switch {
	case couldNotRun(out):
		ev.Ran = false
		ev.Detail = firstMeaningfulLine(out)
	case err != nil:
		ev.Ran = true
		ev.Passed = false
		ev.Detail = firstMeaningfulLine(out)
	case !testPassedIn(out, acceptName):
		ev.Ran = false
		ev.Detail = "pair incomplete: " + acceptName + " did not run"
	case !testPassedIn(out, rejectName):
		ev.Ran = false
		ev.Detail = "pair incomplete: " + rejectName + " did not run"
	default:
		ev.Ran = true
		ev.Passed = true
		ev.Detail = "pair ok: accept=" + acceptName + " reject=" + rejectName
	}
	return ev
}

// testPassedIn reports whether verbose go test output records a top-level pass
// for exactly the named test. The trailing space before the duration excludes
// subtest lines ("--- PASS: TestX/case") and name-prefix collisions.
func testPassedIn(out, name string) bool {
	return strings.Contains(out, "--- PASS: "+name+" ")
}
