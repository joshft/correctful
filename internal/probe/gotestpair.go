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
// Both tests run in a single `go test -json` invocation, and the verdict
// comes from the EVENT STREAM under the same discipline as the single runner
// — never the exit status. The compound shape adds its own hazards on top of
// the single runner's measured ones: `go test` exits 0 when the -run pattern
// matches only one of the two names (the other renamed or deleted), and a
// skipped side also exits 0 — consuming either as a pair pass would confer
// T2 on evidence that was never produced. The pair passes only on an
// explicit "pass" event for EACH name; a "fail" event on either side refutes
// (refutation dominates the other side's state); and everything else — a
// skip, a missing side, a build failure, cancellation — is Ran=false: it
// verifies nothing and refutes nothing.
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
	events := runGoTestJSON(ctx, repoDir, "./"+pkgDir, pattern, false, probeID)
	ev.Duration = time.Since(start).Round(time.Millisecond).String()

	ev.Ran, ev.Passed, ev.Detail = pairVerdictFromEvents(events, acceptName, rejectName)
	return ev
}

// pairVerdictFromEvents reduces one event stream to the compound verdict for
// the two EXACT test names (subtests carry "Parent/sub" names and never
// match).
func pairVerdictFromEvents(events []goTestEvent, acceptName, rejectName string) (ran, passed bool, detail string) {
	terminal := map[string]string{}
	testOut := map[string][]string{}
	var otherOut []string
	for _, e := range events {
		switch {
		case e.Test == acceptName || e.Test == rejectName:
			switch e.Action {
			case "pass", "fail", "skip":
				terminal[e.Test] = e.Action
			case "output":
				testOut[e.Test] = append(testOut[e.Test], e.Output)
			}
		case e.Action == "output" || e.Action == "build-output":
			otherOut = append(otherOut, e.Output)
		}
	}

	sides := []struct{ name, label string }{
		{acceptName, "accept"},
		{rejectName, "reject"},
	}
	// Refutation dominates: a side that executed and failed refutes the
	// claim regardless of what the other side did.
	for _, s := range sides {
		if terminal[s.name] == "fail" {
			return true, false, s.label + " side failed: " + failDetail(strings.Join(testOut[s.name], ""))
		}
	}
	for _, s := range sides {
		switch terminal[s.name] {
		case "pass":
		case "skip":
			return false, false, "pair incomplete: " + s.name + " skipped — a skip asserts nothing"
		default:
			if len(terminal) == 0 {
				// Neither side produced a terminal event: build failure, no
				// matching tests, or cancellation. Nothing executed.
				return false, false, firstMeaningfulLine(strings.Join(otherOut, ""))
			}
			return false, false, "pair incomplete: " + s.name + " did not run"
		}
	}
	return true, true, "pair ok: accept=" + acceptName + " reject=" + rejectName
}
