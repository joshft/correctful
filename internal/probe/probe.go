// Package probe dispatches mechanical checks against harvested claims and
// returns evidence. A probe's job is narrow and total: run, and report whether
// it ran and whether the claim held. The TIER a probe confers is fixed by the
// probe's kind — never negotiated, never an opinion.
package probe

import (
	"context"
	"strings"
	"sync"

	"github.com/joshft/correctful/schema"
)

// A Runner executes one kind of probe.
type Runner interface {
	// CanRun reports whether this runner handles the given probe id.
	CanRun(probeID string) bool
	// MaxTier is the highest evidence tier a pass from this runner confers.
	MaxTier() schema.Tier
	// Run executes probeID for claim and returns evidence. It must set
	// Ran=false (not Passed=false) when the probe could not validly execute —
	// a build error or a missing target refutes nothing.
	Run(ctx context.Context, repoDir string, claim schema.Claim, probeID string) schema.Evidence
}

// Dispatcher fans claims out to the runners that handle their probes.
type Dispatcher struct {
	runners     []Runner
	concurrency int
}

// NewDispatcher builds a dispatcher over the given runners. concurrency < 1 is
// treated as 1.
func NewDispatcher(concurrency int, runners ...Runner) *Dispatcher {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Dispatcher{runners: runners, concurrency: concurrency}
}

// Dispatch runs EVERY bound probe of every claim and returns the evidence
// grouped per claim, in claim order (evidence[i] belongs to claims[i]).
//
// A probe id bound under several claims (a test method of a class named for two
// invariants) EXECUTES ONCE; its evidence is attributed to every claim that
// binds it. One physical run, one verdict — running it twice would waste the
// probe budget and open the door to two evidence rows disagreeing about a
// single execution.
//
// A claim with no bound probes, or a probe no runner handles, yields evidence
// with Ran=false — recorded, not silently dropped. The remainder is computed
// from what did not verify, so an unrunnable probe must leave a trace.
func (d *Dispatcher) Dispatch(ctx context.Context, repoDir string, claims []schema.Claim) [][]schema.Evidence {
	// Pre-pass: mark go-test probes whose claims carry code reference sites —
	// those runs are instrumented for coverage so the attribution pass below
	// can evaluate each edge's binding (see coverage.go).
	for _, c := range claims {
		if !hasGoRefSite(c) {
			continue
		}
		for _, pid := range c.ProbeIDs {
			if strings.HasPrefix(pid, schema.GoTestProbePrefix) {
				coverWanted.Store(pid, true)
			}
		}
	}

	// Collect unique probe ids, keeping a representative claim for each.
	results := map[string]*schema.Evidence{}
	sem := make(chan struct{}, d.concurrency)
	var wg sync.WaitGroup

	for _, c := range claims {
		for _, pid := range c.ProbeIDs {
			if _, seen := results[pid]; seen {
				continue
			}
			ev := &schema.Evidence{ProbeID: pid, Ran: false, Detail: "no runner available for probe kind"}
			results[pid] = ev
			runner := d.runnerFor(pid)
			if runner == nil {
				continue
			}
			wg.Add(1)
			go func(c schema.Claim, pid string, r Runner, slot *schema.Evidence) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				got := r.Run(ctx, repoDir, c, pid)
				// Tier-as-probe-property is load-bearing, so the dispatcher
				// ENFORCES it rather than trusting each runner's bookkeeping:
				// no evidence may exceed its runner's declared maximum.
				if got.Tier > r.MaxTier() {
					got.Tier = r.MaxTier()
				}
				*slot = got
			}(c, pid, runner, ev)
		}
	}
	wg.Wait()

	// Attribute each probe's single verdict to every claim binding it.
	out := make([][]schema.Evidence, len(claims))
	for i, c := range claims {
		if len(c.ProbeIDs) == 0 {
			out[i] = []schema.Evidence{{ClaimID: c.ID, Ran: false, Detail: "no probe bound"}}
			continue
		}
		out[i] = make([]schema.Evidence, len(c.ProbeIDs))
		for j, pid := range c.ProbeIDs {
			ev := *results[pid]
			ev.ClaimID = c.ID
			// Binding is a property of the EDGE: the shared execution's
			// profile is evaluated against THIS claim's reference sites.
			if ev.Ran {
				if p, ok := covProfiles.Load(pid); ok {
					ev.Binding = bindingFor(repoDir, c.RefSites, p.(covProfile))
				}
			}
			out[i][j] = ev
		}
	}
	return out
}

func (d *Dispatcher) runnerFor(probeID string) Runner {
	for _, r := range d.runners {
		if r.CanRun(probeID) {
			return r
		}
	}
	return nil
}

// Default returns the v0 runner set. Order matters where prefixes overlap:
// the pair runner is listed before the single-test runner because "go-test-pair:"
// would otherwise never be reached if a prefix check were sloppy — and listing
// it first makes the intent explicit regardless.
func Default() []Runner {
	return []Runner{GoTestPairRunner{}, GoTestRunner{}, DotnetTestRunner{}, AlloyCheckRunner{}}
}
