package receipt

import (
	"fmt"
	"reflect"
	"regexp"

	"github.com/joshft/correctful/schema"
)

var hexDigestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

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

	if err := validateCoverage(r.Coverage); err != nil {
		return err
	}

	if p := r.Policy; p != nil {
		if !hexDigestRe.MatchString(p.Digest) {
			return fmt.Errorf("policy digest %q is not a sha256 hex digest", p.Digest)
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
		if !rec.Admitted && rec.Accepted != 0 {
			return fmt.Errorf("intake %q accepted %d rows from a document it did not admit", rec.Supplier, rec.Accepted)
		}
	}
	return nil
}

// validateCoverage re-derives the coverage arithmetic from its own file
// rows. SuppressedMentions is set outside the tally and is not derivable.
func validateCoverage(c schema.Coverage) error {
	var claimed, scanned, unread, unreadPolicy int
	for _, fc := range c.Files {
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
	return nil
}
