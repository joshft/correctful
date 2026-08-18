package receipt

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"

	"github.com/joshft/correctful/schema"
)

var hexDigestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// maxSafeInt is 2^53 − 1, the largest integer an IEEE-754 double represents
// exactly. Receipt counts are bounded to it so a JavaScript (or any
// double-based) consumer reads the same integer this tool signed — a value
// above it is a cross-parser differential, demonstrated with policy.rules =
// 9007199254740993 reading back as ...992.
const maxSafeInt = 1<<53 - 1

// inRange reports whether n is a sane non-negative count within the
// exact-integer range every conforming JSON parser shares.
func inRange(n int) bool { return n >= 0 && n <= maxSafeInt }

// ValidateConsistency recomputes every locally derivable field of a receipt
// and rejects any mismatch. The signer runs it before signing and the
// verifier after signature checking, because a signature authenticates
// bytes, not coherence: a wrapper that signs one refuted result with
// Summary.Refuted set to zero would otherwise carry a valid signature past
// GateBlocked, which reads the summary. This catches bugs and inconsistent
// fraud; it cannot catch a key holder that fabricates a CONSISTENT false
// receipt — that trust lives with the key, and the docs say so.
//
// Policy and intake are validated structurally only: their source
// documents are digest-pinned, not embedded, so their findings cannot be
// recomputed here — pretending otherwise would be false assurance.
func ValidateConsistency(r schema.Receipt) error {
	if r.SchemaVersion != schema.SchemaVersion {
		return fmt.Errorf("schema %q is not %q: this build validates only the schema it ships", r.SchemaVersion, schema.SchemaVersion)
	}

	// Every weighed field re-derives from the claim and its evidence rows.
	var verified, refuted, unverified int
	tierCounts := map[string]int{}
	claims := make([]schema.Claim, 0, len(r.Results))
	var remainder []schema.ClaimResult
	for i, res := range r.Results {
		// Validate the evidence BEFORE weighing it: weigh treats any Tier
		// > T0 as verifying strength, so an out-of-range tier (99) would
		// otherwise weigh to a "verified" result at tier 99 that matches
		// its own inflated stated tier and passes. And a row must carry
		// the id of the claim it sits under — evidence for TestX must not
		// be presented as proof of a renamed AUTH-999 claim.
		for j, e := range res.Evidence {
			if e.Tier < schema.T0Unverified || e.Tier > schema.T4Mechanical {
				return fmt.Errorf("result %d (%s) evidence %d: tier %d is outside T0..T4", i, res.Claim.ID, j, e.Tier)
			}
			if e.ClaimID != res.Claim.ID {
				return fmt.Errorf("result %d (%s) evidence %d: claim_id %q does not match its claim", i, res.Claim.ID, j, e.ClaimID)
			}
		}
		status, tier := weigh(res.Claim, res.Evidence)
		if res.Status != status || res.EffectiveTier != tier {
			return fmt.Errorf("result %d (%s): stated %s/%s, evidence weighs to %s/%s", i, res.Claim.ID, res.Status, res.EffectiveTier, status, tier)
		}
		claims = append(claims, res.Claim)
		tierCounts[tier.String()]++
		switch status {
		case schema.StatusVerified:
			verified++
		case schema.StatusRefuted:
			refuted++
		default:
			unverified++
			remainder = append(remainder, res)
		}
	}

	if !reflect.DeepEqual(r.Remainder, remainder) {
		return fmt.Errorf("remainder does not equal the unverified subset of results (%d stated, %d derived)", len(r.Remainder), len(remainder))
	}

	s := r.Summary
	if s.TotalClaims != len(r.Results) || s.Verified != verified || s.Refuted != refuted || s.Unverified != unverified {
		return fmt.Errorf("summary arithmetic (%d/%d/%d/%d) does not match results (%d/%d/%d/%d)",
			s.TotalClaims, s.Verified, s.Refuted, s.Unverified, len(r.Results), verified, refuted, unverified)
	}
	if len(s.TierCounts) != len(tierCounts) {
		return fmt.Errorf("tier counts carry %d labels, results derive %d", len(s.TierCounts), len(tierCounts))
	}
	for label, n := range tierCounts {
		if s.TierCounts[label] != n {
			return fmt.Errorf("tier count %q is %d, results derive %d", label, s.TierCounts[label], n)
		}
	}
	if !reflect.DeepEqual(s.Anchoring, anchoringSummary(claims)) {
		return fmt.Errorf("anchoring summary does not match the claims it summarizes")
	}

	if err := validateCoverage(r.Coverage, r.Change.Files); err != nil {
		return err
	}

	if p := r.Policy; p != nil {
		if !hexDigestRe.MatchString(p.Digest) {
			return fmt.Errorf("policy digest %q is not a sha256 hex digest", p.Digest)
		}
		if !inRange(p.Rules) || !inRange(p.ExemptTestFiles) {
			return fmt.Errorf("policy counts out of range")
		}
		for _, m := range p.Misses {
			if m.File == "" || m.Rule == "" {
				return fmt.Errorf("policy miss with empty file or rule")
			}
		}
	}

	for _, rec := range r.Intake {
		if rec.MaxTier < schema.T1Assertion || rec.MaxTier > schema.T4Mechanical {
			return fmt.Errorf("intake %q states max tier %d outside 1..4", rec.Supplier, rec.MaxTier)
		}
		// The accepted count must be a sane non-negative integer. A
		// negative value slipped the required-supplier gate: GateBlocked
		// now blocks on Accepted <= 0, but the value must also never reach
		// that check malformed.
		if !inRange(rec.Accepted) {
			return fmt.Errorf("intake %q states an out-of-range accepted count %d", rec.Supplier, rec.Accepted)
		}
		if !rec.Admitted && rec.Accepted != 0 {
			return fmt.Errorf("intake %q accepted %d rows from a document it did not admit", rec.Supplier, rec.Accepted)
		}
	}

	if !inRange(r.Coverage.SuppressedMentions) {
		return fmt.Errorf("coverage suppressed-mentions count out of range")
	}
	if a := r.Summary.Anchoring; a != nil {
		if !inRange(a.SpecIDClaims) || !inRange(a.Resolved) || !inRange(a.Ambiguous) || !inRange(a.Orphan) {
			return fmt.Errorf("anchoring counts out of range")
		}
	}
	return nil
}

// validateCoverage re-derives the coverage arithmetic from its own file
// rows AND ties the coverage rows to the change's file set: the harvest
// produces exactly one row per changed file, so a receipt that claims N
// changed files while its coverage accounts for a different set is
// internally inconsistent — the scope a reader trusts and the scope the
// harvest measured must be the same. SuppressedMentions is set outside the
// tally and is not derivable here (its range is checked by the caller).
func validateCoverage(c schema.Coverage, changeFiles []string) error {
	var claimed, scanned, unread, unreadPolicy int
	covFiles := make([]string, 0, len(c.Files))
	for _, fc := range c.Files {
		covFiles = append(covFiles, fc.File)
		switch {
		case fc.Claims > 0:
			claimed++
		case len(fc.ReadBy) > 0:
			scanned++
		default:
			unread++
			if fc.SkipReason == "hidden-path" {
				unreadPolicy++
			}
		}
	}
	if c.Claimed != claimed || c.Scanned != scanned || c.Unread != unread || c.UnreadPolicy != unreadPolicy {
		return fmt.Errorf("coverage arithmetic (%d/%d/%d/%d) does not match its file rows (%d/%d/%d/%d)",
			c.Claimed, c.Scanned, c.Unread, c.UnreadPolicy, claimed, scanned, unread, unreadPolicy)
	}
	change := append([]string(nil), changeFiles...)
	sort.Strings(change)
	sort.Strings(covFiles)
	if !reflect.DeepEqual(change, covFiles) {
		return fmt.Errorf("coverage accounts for %d files but the change lists %d — the measured scope and the stated scope differ", len(covFiles), len(change))
	}
	return nil
}
