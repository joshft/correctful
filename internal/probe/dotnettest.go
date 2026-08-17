package probe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/joshft/correctful/schema"
)

// DotnetTestRunner executes a single .NET test method (or one [Theory] with all
// its cases) as a probe via `dotnet test --filter`. A pass confers T1.
//
// Verdicts come from the VSTest SUMMARY COUNTS, never from the exit status:
// measured empirically, `dotnet test --filter` EXITS 0 when the filter matches
// no test at all ("No test matches the given testcase filter"). Trusting the
// exit code would record a pass for a phantom or renamed test — evidence that
// was never produced. The probe passes only when the summary reports
// failed == 0 AND passed >= 1.
type DotnetTestRunner struct{}

func (DotnetTestRunner) CanRun(probeID string) bool {
	return strings.HasPrefix(probeID, schema.DotnetTestProbePrefix)
}

func (DotnetTestRunner) MaxTier() schema.Tier { return schema.T1Assertion }

// dotnetPath resolves the dotnet CLI once: PATH first, then the conventional
// user-local install (~/.dotnet/dotnet).
var dotnetPath = sync.OnceValue(func() string {
	if p, err := exec.LookPath("dotnet"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".dotnet", "dotnet")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
})

// projLocks serializes probe runs per .csproj: concurrent `dotnet test`
// invocations of the same project race on its obj/ and bin/ build outputs.
// Probes of different projects (and non-dotnet probes) still run in parallel.
var projLocks sync.Map // csproj path -> *sync.Mutex

func (DotnetTestRunner) Run(ctx context.Context, repoDir string, claim schema.Claim, probeID string) schema.Evidence {
	ev := schema.Evidence{ClaimID: claim.ID, ProbeID: probeID, Tier: schema.T1Assertion}

	csproj, classDotMethod, ok := schema.ParseDotnetTestProbeID(probeID)
	if !ok {
		ev.Detail = "malformed dotnet-test probe id"
		return ev
	}
	dotnet := dotnetPath()
	if dotnet == "" {
		ev.Detail = "dotnet CLI not found (PATH or ~/.dotnet)"
		return ev
	}

	mu, _ := projLocks.LoadOrStore(csproj, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	start := time.Now()
	cmd := exec.CommandContext(ctx, dotnet, "test", csproj,
		"--filter", "FullyQualifiedName~"+classDotMethod)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	ev.Duration = time.Since(start).Round(time.Millisecond).String()

	ev.Ran, ev.Passed, ev.Detail = interpretDotnetOutput(string(out), err)
	return ev
}

// dotnetSummaryRe matches the VSTest result line, e.g.:
//
//	Passed!  - Failed:     0, Passed:     1, Skipped:     0, Total:     1, Duration: 4 ms - Example.Gate.Tests.dll (net10.0)
var dotnetSummaryRe = regexp.MustCompile(`Failed:\s*(\d+),\s*Passed:\s*(\d+),\s*Skipped:\s*(\d+),\s*Total:\s*(\d+)`)

// interpretDotnetOutput reduces a `dotnet test` run to (ran, passed, detail)
// from its output text, honoring the summary counts over the exit status.
func interpretDotnetOutput(out string, runErr error) (ran, passed bool, detail string) {
	if strings.Contains(out, "No test matches the given testcase filter") {
		return false, false, "no test matches filter (phantom or renamed test)"
	}
	m := dotnetSummaryRe.FindStringSubmatch(out)
	if m == nil {
		// No summary at all: the run never reached test execution — almost
		// always a build failure. It refutes nothing.
		return false, false, dotnetFailDetail(out, "no test summary in output")
	}
	failed, passedN := m[1], m[2]
	switch {
	case failed != "0":
		return true, false, dotnetFailDetail(out, "failed: "+failed)
	case passedN == "0":
		// Total > 0 with zero passed and zero failed: everything was skipped.
		return false, false, "matched test(s) were skipped, none executed"
	case runErr != nil:
		// Counts say pass but the process failed — trust neither; be honest.
		return false, false, dotnetFailDetail(out, "exit error despite passing summary")
	default:
		return true, true, "passed " + passedN + " test(s)"
	}
}

// dotnetFailDetail extracts the most informative line: an xunit [FAIL] line, a
// compiler error, or the summary — else the fallback.
func dotnetFailDetail(out, fallback string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "[FAIL]") || strings.Contains(l, "error CS") ||
			strings.HasPrefix(l, "Failed!") || strings.Contains(l, "Build FAILED") {
			return truncate(l, 200)
		}
	}
	return fallback
}
