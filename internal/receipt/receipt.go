// Package receipt assembles claims and evidence into a schema.Receipt and
// renders it. The remainder — the claims nothing verified — is computed here and
// surfaced as its own field, never left for a reader to derive.
package receipt

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/schema"
)

// Assemble joins claims with their evidence (grouped per claim, index-aligned)
// and computes each claim's status, effective tier, the remainder, and the
// summary. Coverage — the harvest's own disclosure of what it could and could
// not read — is carried into the receipt verbatim.
func Assemble(change gitdiff.Change, claims []schema.Claim, evidence [][]schema.Evidence, coverage schema.Coverage) schema.Receipt {
	results := make([]schema.ClaimResult, 0, len(claims))
	var remainder []schema.ClaimResult
	tierCounts := map[string]int{}
	var verified, refuted, unverified int

	for i, c := range claims {
		var evs []schema.Evidence
		if i < len(evidence) {
			evs = evidence[i]
		}
		for j := range evs {
			evs[j].Detail = sanitizePaths(evs[j].Detail, change.Repo)
		}
		status, tier := weigh(evs)

		res := schema.ClaimResult{
			Claim:         c,
			Status:        status,
			EffectiveTier: tier,
			Evidence:      evs,
		}
		results = append(results, res)
		tierCounts[tier.String()]++

		switch status {
		case schema.StatusVerified:
			verified++
		case schema.StatusRefuted:
			refuted++
		case schema.StatusUnverified:
			unverified++
			remainder = append(remainder, res)
		}
	}

	return schema.Receipt{
		SchemaVersion: schema.SchemaVersion,
		Change: schema.ChangeRef{
			// The receipt carries the repository NAME, never its location: a
			// receipt is shareable, and external-tool details are scrubbed of
			// local paths for the same reason (see sanitizePaths).
			Repo:    filepath.Base(change.Repo),
			BaseRef: change.BaseRef,
			HeadRef: change.HeadRef,
			BaseSHA: change.BaseSHA,
			HeadSHA: change.HeadSHA,
			Files:   change.Files,
		},
		Results:   results,
		Remainder: remainder,
		Coverage:  coverage,
		Summary: schema.Summary{
			TotalClaims: len(claims),
			Verified:    verified,
			Refuted:     refuted,
			Unverified:  unverified,
			TierCounts:  tierCounts,
			Anchoring:   anchoringSummary(claims),
		},
	}
}

// anchoringSummary tallies the binding layer's resolution outcomes. Nil when
// no claim carries an anchor — a repo without a definition corpus.
func anchoringSummary(claims []schema.Claim) *schema.AnchoringSummary {
	var a schema.AnchoringSummary
	for _, c := range claims {
		if c.Anchor == nil {
			continue
		}
		a.SpecIDClaims++
		switch c.Anchor.Status {
		case schema.AnchorResolved:
			a.Resolved++
		case schema.AnchorAmbiguous:
			a.Ambiguous++
		case schema.AnchorOrphan:
			a.Orphan++
		}
	}
	if a.SpecIDClaims == 0 {
		return nil
	}
	return &a
}

// bindingNote reduces a claim's evidence to a binding marker for its row.
// "coverage-proven" wins as soon as ANY probe demonstrably reached an
// annotated reference site; "name-only" means the binding was checked for at
// least one probe and no annotated region was reached; no marker means no
// coverage check applied.
func bindingNote(res schema.ClaimResult) string {
	nameOnly := false
	for _, e := range res.Evidence {
		switch e.Binding {
		case "covered":
			return "  [binding: coverage-proven]"
		case "name-only":
			nameOnly = true
		}
	}
	if nameOnly {
		return "  [binding: name-only]"
	}
	return ""
}

// llmNote marks an LLM-proposed claim wherever it renders: a reader must
// never mistake a model's proposal for something the change wrote down.
func llmNote(c schema.Claim) string {
	if c.Source.Kind == schema.SourceLLM {
		return "  [llm-proposed]"
	}
	return ""
}

// anchorNote renders a claim's anchor state as a row suffix. Resolved claims
// need no marker — their upgraded text IS the definition. The marker calls
// out the two states a reader should distrust: an id nothing defines, and an
// id defined incompatibly in several places.
func anchorNote(c schema.Claim) string {
	if c.Anchor == nil {
		return ""
	}
	switch c.Anchor.Status {
	case schema.AnchorOrphan:
		return "  [orphan id: defined nowhere in the spec corpus]"
	case schema.AnchorAmbiguous:
		return fmt.Sprintf("  [ambiguous id: %d definitions in the corpus]", len(c.Anchor.Sites))
	}
	return ""
}

// shaNote renders the immutable pins beside the symbolic refs, abbreviated.
func shaNote(c schema.ChangeRef) string {
	short := func(s string) string {
		if len(s) > 12 {
			return s[:12]
		}
		return s
	}
	switch {
	case c.BaseSHA != "" && c.HeadSHA != "":
		return fmt.Sprintf(" (%s..%s)", short(c.BaseSHA), short(c.HeadSHA))
	case c.HeadSHA != "":
		return fmt.Sprintf(" (@%s)", short(c.HeadSHA))
	}
	return ""
}

// sanitizePaths scrubs local filesystem locations from probe detail text: the
// repository root becomes "." and the home directory "~". External tools
// (compilers, test runners) print absolute paths freely, and a receipt that
// echoes them leaks machine layout into a shareable artifact.
func sanitizePaths(detail, repoRoot string) string {
	if detail == "" {
		return detail
	}
	if repoRoot != "" && repoRoot != "." {
		detail = strings.ReplaceAll(detail, repoRoot, ".")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		detail = strings.ReplaceAll(detail, home, "~")
	}
	return detail
}

// weigh reduces a claim's evidence to a status and an effective tier.
//
//   - refuted:  ANY probe ran and failed. Refutation dominates — a claim bound
//     by five tests where four pass and one fails does NOT hold, and letting
//     passing probes outvote a failing one would launder a defect into a
//     verified row.
//   - verified: no refutation, and some probe ran and passed; effective tier is
//     the highest a passing probe conferred.
//   - unverified: nothing ran that could raise the claim. Remainder.
func weigh(evs []schema.Evidence) (schema.Status, schema.Tier) {
	best := schema.T0Unverified
	anyVerified, anyRefuted := false, false
	for _, e := range evs {
		switch {
		case e.Verified():
			anyVerified = true
			if e.Tier > best {
				best = e.Tier
			}
		case e.Refuted():
			anyRefuted = true
		}
	}
	switch {
	case anyRefuted:
		return schema.StatusRefuted, schema.T0Unverified
	case anyVerified:
		return schema.StatusVerified, best
	default:
		return schema.StatusUnverified, schema.T0Unverified
	}
}

// WriteJSON emits the receipt as indented JSON — the payload other tools read.
func WriteJSON(w io.Writer, r schema.Receipt) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText renders the receipt for a human at a terminal. The remainder gets
// its own section because it is the part a reader most needs to see and the part
// every other tool hides.
func WriteText(w io.Writer, r schema.Receipt) {
	s := r.Summary
	fmt.Fprintf(w, "correctful receipt (schema %s)\n", r.SchemaVersion)
	fmt.Fprintf(w, "change: %s...%s%s", r.Change.BaseRef, r.Change.HeadRef, shaNote(r.Change))
	if r.Change.Repo != "" {
		fmt.Fprintf(w, "  [%s]", r.Change.Repo)
	}
	fmt.Fprintf(w, "\nfiles: %d changed\n\n", len(r.Change.Files))

	fmt.Fprintf(w, "claims: %d   verified: %d   refuted: %d   unverified: %d\n",
		s.TotalClaims, s.Verified, s.Refuted, s.Unverified)
	fmt.Fprintf(w, "tiers:")
	for _, t := range []schema.Tier{schema.T4Mechanical, schema.T3Property, schema.T2Adversarial, schema.T1Assertion, schema.T0Unverified} {
		if n := s.TierCounts[t.String()]; n > 0 {
			fmt.Fprintf(w, "  %s=%d", t.String(), n)
		}
	}
	fmt.Fprintln(w)
	if a := s.Anchoring; a != nil {
		fmt.Fprintf(w, "anchoring: %d spec-id claims — %d resolved to definitions, %d ambiguous, %d orphan\n",
			a.SpecIDClaims, a.Resolved, a.Ambiguous, a.Orphan)
	}
	fmt.Fprintln(w)

	if s.Verified > 0 {
		fmt.Fprintln(w, "VERIFIED")
		for _, res := range r.Results {
			if res.Status == schema.StatusVerified {
				fmt.Fprintf(w, "  [%s] %s — %s%s%s\n", res.EffectiveTier, res.Claim.ID, res.Claim.Text,
					anchorNote(res.Claim), bindingNote(res))
			}
		}
		fmt.Fprintln(w)
	}
	if s.Refuted > 0 {
		fmt.Fprintln(w, "REFUTED (a probe ran and the claim did not hold — the gate blocks here)")
		for _, res := range r.Results {
			if res.Status == schema.StatusRefuted {
				fmt.Fprintf(w, "  %s — %s\n", res.Claim.ID, detailOf(res))
			}
		}
		fmt.Fprintln(w)
	}

	// The headline. Always printed, even when empty, so its absence is a stated
	// result and not an omission.
	fmt.Fprintf(w, "UNVERIFIED REMAINDER (%d) — what nothing checked\n", len(r.Remainder))
	if len(r.Remainder) == 0 {
		fmt.Fprintln(w, "  (empty — every harvested claim reached a probe)")
	}
	for _, res := range r.Remainder {
		fmt.Fprintf(w, "  %s — %s  [%s:%d]%s%s\n", res.Claim.ID, res.Claim.Text,
			res.Claim.Source.File, res.Claim.Source.Line, anchorNote(res.Claim), llmNote(res.Claim))
	}

	writeCoverage(w, r.Coverage)
}

// writeCoverage renders the harvest's self-disclosure: the same honesty the
// remainder applies to claims, applied to the tool's own reach. Always printed
// — a receipt that verified everything it harvested while reading almost
// nothing must say so in the same breath.
func writeCoverage(w io.Writer, cov schema.Coverage) {
	fmt.Fprintf(w, "\nHARVEST COVERAGE — %d files: %d claimed, %d scanned (read, no claims), %d unread\n",
		len(cov.Files), cov.Claimed, cov.Scanned, cov.Unread)
	// Group the blind spots by extension so the reader sees WHAT kind of
	// content no harvester could read, without 400 lines of file list. The
	// histogram is shared with the markdown renderer — one computation, no
	// drift between the two disclosures.
	if hist := unreadHistogram(cov); hist != "" {
		fmt.Fprintf(w, "  unread (no harvester for): %s\n", hist)
	}
}

// detailOf picks the evidence detail a reader needs: for a refuted claim, the
// FAILING probe's detail — a claim with five probes where the fourth failed
// must not display the first probe's "ok".
func detailOf(res schema.ClaimResult) string {
	for _, e := range res.Evidence {
		if e.Refuted() && e.Detail != "" {
			return e.Detail
		}
	}
	if len(res.Evidence) > 0 && res.Evidence[0].Detail != "" {
		return res.Evidence[0].Detail
	}
	return res.Claim.Text
}
