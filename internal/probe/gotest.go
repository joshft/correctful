package probe

import (
	"context"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/joshft/correctful/schema"
)

// GoTestRunner executes a single Go test as a probe. A pass confers T1: one
// assertion held. It does not confer more — under-claiming the tier is the safe
// direction for a checker. The compound accept/reject shape that earns T2 is
// GoTestPairRunner's job.
type GoTestRunner struct{}

func (GoTestRunner) CanRun(probeID string) bool {
	return strings.HasPrefix(probeID, schema.GoTestProbePrefix)
}

func (GoTestRunner) MaxTier() schema.Tier { return schema.T1Assertion }

func (GoTestRunner) Run(ctx context.Context, repoDir string, claim schema.Claim, probeID string) schema.Evidence {
	ev := schema.Evidence{ClaimID: claim.ID, ProbeID: probeID, Tier: schema.T1Assertion}

	file, name, ok := schema.ParseGoTestProbeID(probeID)
	if !ok {
		ev.Detail = "malformed go-test probe id"
		return ev
	}
	pkgDir := "./" + path.Dir(file)

	start := time.Now()
	out, err := runGoTest(ctx, repoDir, pkgDir, "^"+regexp.QuoteMeta(name)+"$", false)
	ev.Duration = time.Since(start).Round(time.Millisecond).String()

	switch {
	case couldNotRun(out):
		// Build error, no matching test, or no test files — the probe did not
		// validly execute, so it refutes nothing.
		ev.Ran = false
		ev.Detail = firstMeaningfulLine(out)
	case err == nil:
		ev.Ran = true
		ev.Passed = true
		ev.Detail = "ok " + pkgDir
	default:
		ev.Ran = true
		ev.Passed = false
		ev.Detail = firstMeaningfulLine(out)
	}
	return ev
}

// runGoTest invokes `go test` for a -run pattern in pkgDir under repoDir.
// -count=1 ensures a fresh execution rather than a cached verdict.
func runGoTest(ctx context.Context, repoDir, pkgDir, runPattern string, verbose bool) (string, error) {
	args := []string{"test", "-run", runPattern, "-count=1"}
	if verbose {
		args = append(args, "-v")
	}
	args = append(args, pkgDir)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// couldNotRunMarkers are substrings that mean the probe never validly executed
// (build error, missing target). They are deliberately narrow: a marker that
// also appears in ordinary assertion output would let a real refutation be
// laundered into "did not run", which is the worst failure a gate can have.
var couldNotRunMarkers = []string{
	"no tests to run",
	"no test files",
	"[build failed]",
	"no required module provides",
}

func couldNotRun(out string) bool {
	l := strings.ToLower(out)
	for _, m := range couldNotRunMarkers {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// firstMeaningfulLine returns a short summary line for the receipt: the first
// FAIL/ok/error line, else the first non-empty line.
func firstMeaningfulLine(out string) string {
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "--- FAIL") || strings.HasPrefix(l, "FAIL") ||
			strings.HasPrefix(l, "ok ") || strings.Contains(l, "error") {
			return truncate(l, 200)
		}
	}
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			return truncate(l, 200)
		}
	}
	return "(no output)"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
