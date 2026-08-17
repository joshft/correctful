// Package harvest is the v0 claims path: it reads the files a change touches and
// HARVESTS the claims that are already written into them — test names, spec
// identifiers, (later) Alloy asserts and RFC MUST clauses.
//
// Harvesting is deliberately the first extractor correctful ships. It carries
// zero extraction risk: the claims already exist as artifacts a human wrote and
// a machine can read verbatim. The harder cases — inferring claims a diff makes
// implicitly (structural heuristics, then an LLM) — are later increments, each
// gated on beating the harvest path it builds on.
package harvest

import (
	"regexp"
	"strings"

	"github.com/joshft/correctful/schema"
)

// Result is what one harvester produced from a file set: the claims it
// proposed and the files it actually opened and understood. Read is reported
// from inside the harvester's own read path — never from a parallel
// "would-I-read-this" predicate, which would inevitably drift from the real
// filter — so the receipt's coverage disclosure cannot disagree with what
// happened.
type Result struct {
	Claims []schema.Claim
	Read   []string // repo-relative files this harvester read
}

// A Harvester reads a set of changed files under repoDir and proposes claims.
type Harvester interface {
	// Name identifies the harvester in logs and receipts.
	Name() string
	// Harvest proposes claims from the given files. Files are repo-relative.
	Harvest(repoDir string, files []string) (Result, error)
}

// Run executes every harvester, unions their claims, and reconciles duplicates.
//
// Reconciliation MERGES claims with the same normalized ID: they describe the
// same underlying claim, so their bound probes are unioned rather than the
// duplicates being dropped. Five tests named for INV-008 yield ONE claim with
// five probes — all of which run, any of which can refute. A probe-less
// spec-reference claim merging with a probed test claim adopts the test claim's
// identity; unmatched references stay probe-less and land in the remainder.
//
// After the merge, pair detection (DetectPairs) collapses an accept-polarity
// and a reject-polarity test of the same claim into one compound pair probe,
// which is how a claim earns T2.
//
// Run also computes the receipt's COVERAGE disclosure: per changed file, which
// harvesters read it and how many claims it sourced (counted pre-merge, so a
// file whose claims reconciled into another file's claim still counts as
// contributing). Files no harvester read are the tool's own blind spots, and
// the receipt states them the way it states the remainder.
func Run(repoDir string, files []string, harvesters ...Harvester) ([]schema.Claim, schema.Coverage, error) {
	byID := map[string]schema.Claim{}
	var order []string // preserve first-seen order for stable output

	add := func(c schema.Claim) {
		existing, seen := byID[c.ID]
		if !seen {
			byID[c.ID] = c
			order = append(order, c.ID)
			return
		}
		// A probe-bearing claim supersedes a probe-less placeholder's identity;
		// otherwise first-seen identity wins. Probes always union.
		if len(existing.ProbeIDs) == 0 && len(c.ProbeIDs) > 0 {
			byID[c.ID] = c
			return
		}
		existing.ProbeIDs = unionStrings(existing.ProbeIDs, c.ProbeIDs)
		byID[c.ID] = existing
	}

	readBy := map[string][]string{}
	claimCount := map[string]int{}

	for _, h := range harvesters {
		res, err := h.Harvest(repoDir, files)
		if err != nil {
			return nil, schema.Coverage{}, err
		}
		for _, f := range res.Read {
			readBy[f] = append(readBy[f], h.Name())
		}
		for _, c := range res.Claims {
			claimCount[c.Source.File]++
			add(c)
		}
	}

	out := make([]schema.Claim, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}

	cov := schema.Coverage{Files: make([]schema.FileCoverage, 0, len(files))}
	for _, f := range files {
		fc := schema.FileCoverage{File: f, ReadBy: readBy[f], Claims: claimCount[f]}
		cov.Files = append(cov.Files, fc)
		switch {
		case fc.Claims > 0:
			cov.Claimed++
		case len(fc.ReadBy) > 0:
			cov.Scanned++
		default:
			cov.Unread++
		}
	}
	return DetectPairs(out), cov, nil
}

// unionStrings appends the elements of b not already in a, preserving order.
func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			seen[x] = true
			a = append(a, x)
		}
	}
	return a
}

// Default returns the v0 harvester set. Test harvesters come before the
// spec-reference harvester so probe-bearing claims establish identity first.
func Default() []Harvester {
	return []Harvester{GoTestHarvester{}, CSharpTestHarvester{}, AlloyHarvester{}, RFCMustHarvester{}, SpecRefHarvester{}}
}

// --- shared helpers used by more than one harvester ---

// specIDRe matches a methodology identifier: a class prefix followed by 2–4
// digits, with an optional separator. The prefix set is specific enough that
// false positives in real source are rare (INV-009, PRH_004, ABS042, TB-001).
//
// The trailing "not a digit, or end" clause — rather than a plain \b — is what
// lets an identifier embedded in a test name match, where the next character is
// an underscore or an uppercase letter (INV009_Zero, INV009Zero): those are word
// characters, so \b would not fire after the digits. The clause consumes one
// trailing byte, which is harmless because callers read only the capture groups.
var specIDRe = regexp.MustCompile(`\b(INV|BND|PRH|ABS|PAT|TB|AP)[-_]?(\d{2,4})(?:[^0-9]|$)`)

// normalizeSpecID canonicalizes a raw identifier to `PREFIX-NNN` form, or
// returns "" if the token is not a spec identifier.
func normalizeSpecID(raw string) string {
	m := specIDRe.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	return m[1] + "-" + m[2]
}

// segmentIDRe matches a single name SEGMENT that is itself a spec identifier,
// preserving a trailing sub-variant letter (INV004b, INV007a). Anchored ^…$ so
// it matches a whole segment, not a substring — this is what makes segment-based
// name detection precise where a substring scan would over-match.
//
// Case-insensitive because naming conventions vary by language: Go tests write
// TestINV009_…, C# test classes write Inv018PinnedPolicyTests / TrustBoundary
// Tb007Tests. The anchoring still protects precision — "Band010" and
// "Inventory018" are single segments and match nothing. Output is canonical:
// prefix uppercased, sub-variant letter lowercased.
var segmentIDRe = regexp.MustCompile(`(?i)^(INV|BND|PRH|ABS|PAT|TB|AP)[-_]?(\d{2,4})([a-z]?)$`)

// segmentAndRe matches an id-continuation segment: "And010" in a name like
// Inv009And010ProbesTests, which declares a SECOND id sharing the previous
// segment's prefix. Only meaningful directly after a matched id segment.
var segmentAndRe = regexp.MustCompile(`(?i)^and[-_]?(\d{2,4})([a-z]?)$`)

// specIDFromSegment returns the canonical id for a segment that IS a spec
// identifier (INV004b -> INV-004b, Inv018 -> INV-018), or "" otherwise.
func specIDFromSegment(seg string) string {
	m := segmentIDRe.FindStringSubmatch(seg)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1]) + "-" + m[2] + strings.ToLower(m[3])
}

// specIDsInName returns the spec identifiers embedded as whole segments of a
// (Test-stripped) test name, in order, de-duplicated.
//
// Segment matching — splitting the name on separators and camelCase humps, then
// testing each segment whole — recognizes an id ANYWHERE in the name, including
// after a cluster prefix (ClusterC_INV004b_…), which a leading-\b substring scan
// silently misses. The precision comes from requiring the id to be its own
// segment: "MAINV04LUE" is one segment and matches nothing.
//
// A continuation segment ("And010") directly following a matched id extends
// that id's prefix — Inv009And010 declares both INV-009 and INV-010.
func specIDsInName(nameBody string) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	lastPrefix := ""
	for _, seg := range strings.Fields(humanize(nameBody)) {
		if id := specIDFromSegment(seg); id != "" {
			add(id)
			lastPrefix = id[:strings.Index(id, "-")]
			continue
		}
		if lastPrefix != "" {
			if m := segmentAndRe.FindStringSubmatch(seg); m != nil {
				add(lastPrefix + "-" + m[1] + strings.ToLower(m[2]))
				continue
			}
		}
		lastPrefix = ""
	}
	return ids
}

// humanize turns a CamelCase / snake_case token into a spaced phrase.
// "ZeroValuePolicyRejected" -> "Zero Value Policy Rejected".
func humanize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			// Insert a space at a lower->upper or letter->digit boundary.
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
}
