package harvest

import (
	"path"
	"strings"

	"github.com/joshft/correctful/schema"
)

// Pair detection: a claim bound by BOTH an accept-polarity test and a
// reject-polarity test (in the same package) is being tested adversarially —
// the positive case must hold AND the negative case must be rejected. Those two
// tests collapse into one compound pair probe, whose runner confers T2.
//
// Polarity is read from the test NAME's segments against small exact-match
// vocabularies. The vocabularies are deliberately conservative — the same
// precision-over-recall rule that cut doc-comment binding applies here: a word
// like "Fails" is excluded because it names an outcome, not a polarity
// ("FailsClosed" rejects bad input, "FailsToStart" is a defect scenario), and a
// mis-paired probe would mint an unearned T2. A name matching BOTH vocabularies
// is ambiguous and classified as neither.

var rejectWords = map[string]bool{
	"Reject": true, "Rejects": true, "Rejected": true,
	"Deny": true, "Denies": true, "Denied": true,
	"Refuse": true, "Refuses": true, "Refused": true,
	"Invalid": true,
	"Block":   true, "Blocks": true, "Blocked": true,
	"Forbid": true, "Forbids": true, "Forbidden": true,
}

var acceptWords = map[string]bool{
	"Accept": true, "Accepts": true, "Accepted": true,
	"Allow": true, "Allows": true, "Allowed": true,
	"Valid": true, "Honored": true, "Honors": true,
	"Succeeds": true, "Passes": true,
}

// testPolarity classifies a test name: +1 accept, -1 reject, 0 unknown or
// ambiguous (both polarities present).
func testPolarity(name string) int {
	hasAccept, hasReject := false, false
	for _, seg := range strings.Fields(humanize(testBody(name))) {
		if acceptWords[seg] {
			hasAccept = true
		}
		if rejectWords[seg] {
			hasReject = true
		}
	}
	switch {
	case hasAccept && hasReject:
		return 0
	case hasAccept:
		return +1
	case hasReject:
		return -1
	default:
		return 0
	}
}

// DetectPairs rewrites each multi-probe claim, collapsing the first
// accept/reject test pair that shares a package into one pair probe. All other
// probes are kept — every binding test still runs, and any failure refutes.
func DetectPairs(claims []schema.Claim) []schema.Claim {
	for i := range claims {
		if len(claims[i].ProbeIDs) >= 2 {
			claims[i].ProbeIDs = pairProbes(claims[i].ProbeIDs)
		}
	}
	return claims
}

type boundTest struct {
	probeID string
	pkgDir  string
	name    string
}

func pairProbes(probeIDs []string) []string {
	var accepts, rejects []boundTest
	var rest []string

	for _, pid := range probeIDs {
		file, name, ok := schema.ParseGoTestProbeID(pid)
		if !ok {
			rest = append(rest, pid)
			continue
		}
		bt := boundTest{probeID: pid, pkgDir: path.Dir(file), name: name}
		switch testPolarity(name) {
		case +1:
			accepts = append(accepts, bt)
		case -1:
			rejects = append(rejects, bt)
		default:
			rest = append(rest, pid)
		}
	}

	for ai, a := range accepts {
		for ri, r := range rejects {
			if a.pkgDir != r.pkgDir {
				continue
			}
			out := []string{schema.GoTestPairProbeID(a.pkgDir, a.name, r.name)}
			for i, x := range accepts {
				if i != ai {
					out = append(out, x.probeID)
				}
			}
			for i, x := range rejects {
				if i != ri {
					out = append(out, x.probeID)
				}
			}
			return append(out, rest...)
		}
	}
	return probeIDs
}
