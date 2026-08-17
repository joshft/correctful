package probe

import (
	"context"
	"encoding/json"
	"os"
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
//
// Verdicts come from the `go test -json` EVENT STREAM, never the exit status:
// measured empirically, a test that calls t.Skip exits 0 — trusting the exit
// code would confer T1 on a test that asserted nothing. The probe passes only
// on an explicit "pass" event for the exact test, is refuted only on an
// explicit "fail" event, and everything else — skip, no matching test, build
// failure, cancellation — is Ran=false: it refutes nothing and verifies
// nothing.
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
	pattern := "^" + regexp.QuoteMeta(name) + "$"

	start := time.Now()
	_, wantCover := coverWanted.Load(probeID)
	events := runGoTestJSON(ctx, repoDir, pkgDir, pattern, wantCover, probeID)
	if wantCover && !hasTestEvent(events, name) {
		// The instrumented build set can fail where the plain one would not.
		// Instrumentation must never degrade the verdict: fall back to a
		// plain run and simply carry no binding statement.
		events = runGoTestJSON(ctx, repoDir, pkgDir, pattern, false, probeID)
	}
	ev.Duration = time.Since(start).Round(time.Millisecond).String()

	ev.Ran, ev.Passed, ev.Detail = verdictFromEvents(events, name, pkgDir)
	return ev
}

// goTestEvent is one line of `go test -json` output. Test-scoped events carry
// the test name; package and build events do not.
type goTestEvent struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
	Output string `json:"Output"`
}

// runGoTestJSON executes the probe and parses the event stream. With cover
// set, the run is instrumented and its profile stored for the binding pass.
func runGoTestJSON(ctx context.Context, repoDir, pkgDir, pattern string, cover bool, probeID string) []goTestEvent {
	args := []string{"test", "-json", "-run", pattern, "-count=1"}
	var profPath string
	if cover {
		prof, err := os.CreateTemp("", "correctful-cov-*.out")
		if err == nil {
			profPath = prof.Name()
			prof.Close()
			defer os.Remove(profPath)
			args = append(args, "-coverprofile="+profPath, "-coverpkg=./...")
		}
	}
	args = append(args, pkgDir)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = repoDir
	out, _ := cmd.CombinedOutput() // exit status is deliberately unused

	var events []goTestEvent
	for _, line := range strings.Split(string(out), "\n") {
		var e goTestEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // interleaved non-JSON noise carries no verdict
		}
		events = append(events, e)
	}
	if profPath != "" {
		if data, err := os.ReadFile(profPath); err == nil && len(data) > 0 {
			covProfiles.Store(probeID, parseCoverProfile(string(data)))
		}
	}
	return events
}

// hasTestEvent reports whether any terminal event exists for the exact test.
func hasTestEvent(events []goTestEvent, name string) bool {
	for _, e := range events {
		if e.Test == name && (e.Action == "pass" || e.Action == "fail" || e.Action == "skip") {
			return true
		}
	}
	return false
}

// verdictFromEvents reduces the event stream to the probe verdict for the
// EXACT test name (subtests carry "Parent/sub" names and never match).
func verdictFromEvents(events []goTestEvent, name, pkgDir string) (ran, passed bool, detail string) {
	var testOut, otherOut []string
	terminal := ""
	for _, e := range events {
		switch {
		case e.Test == name:
			switch e.Action {
			case "pass", "fail", "skip":
				terminal = e.Action
			case "output":
				testOut = append(testOut, e.Output)
			}
		case e.Action == "output" || e.Action == "build-output":
			otherOut = append(otherOut, e.Output)
		}
	}
	switch terminal {
	case "pass":
		return true, true, "ok " + pkgDir
	case "fail":
		return true, false, failDetail(strings.Join(testOut, ""))
	case "skip":
		return false, false, "test skipped — a skip asserts nothing"
	default:
		// No terminal event for the test: never matched, build failed, or the
		// run was cancelled. Nothing executed, so nothing is refuted.
		return false, false, firstMeaningfulLine(strings.Join(otherOut, ""))
	}
}

// failDetail extracts the failing test's OWN message — the "file.go:12: …"
// assertion line — falling back to the "--- FAIL" banner only when the test
// produced no message of its own.
func failDetail(out string) string {
	fallback := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "=== ") {
			continue
		}
		if strings.HasPrefix(l, "--- FAIL") {
			if fallback == "" {
				fallback = l
			}
			continue
		}
		return truncate(l, 200)
	}
	if fallback != "" {
		return fallback
	}
	return "(no output)"
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
