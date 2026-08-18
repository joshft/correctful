// Package receipt assembles claims and evidence into a schema.Receipt and
// renders it. The remainder — the claims nothing verified — is computed here and
// surfaced as its own field, never left for a reader to derive.
package receipt

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"

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
		status, tier := weigh(c, evs)

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
		ToolVersion:   toolVersion(),
		Change: schema.ChangeRef{
			// The receipt carries the repository NAME, never its location: a
			// receipt is shareable, and external-tool details are scrubbed of
			// local paths for the same reason (see sanitizePaths).
			Repo:        filepath.Base(change.Repo),
			BaseRef:     change.BaseRef,
			HeadRef:     change.HeadRef,
			BaseSHA:     change.BaseSHA,
			HeadSHA:     change.HeadSHA,
			Files:       change.Files,
			Excluded:    exclusions(change.Excluded),
			InputDigest: change.InputDigest,
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

// toolVersion identifies this checker build from its own build info — the
// first chain field: two receipts are comparable across time only when the
// harvesters and runners that produced them are known. Module version plus
// short VCS revision when the build is stamped (`go install` from a clone
// stamps both; a dirty tree gains "+dirty"), "unknown" when the build
// carries no identity — stated, never guessed.
var toolVersion = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := bi.Main.Version
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev != "" && dirty {
		rev += "+dirty"
	}
	switch {
	case v != "" && rev != "":
		return v + " " + rev
	case v != "":
		return v
	case rev != "":
		return rev
	default:
		return "unknown"
	}
})

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
	nameOnly, external := false, ""
	for _, e := range res.Evidence {
		switch e.Binding {
		case schema.BindingCovered:
			return "  [binding: coverage-proven]"
		case schema.BindingFileCovered:
			return "  [binding: file-coverage-proven]"
		case schema.BindingNameOnly:
			nameOnly = true
		case schema.BindingSupplierAttested:
			if e.CountsFor(res.Claim) {
				external = e.Supplier
			}
		}
	}
	switch {
	case external != "":
		return "  [external: " + external + " — supplier-attested]"
	case nameOnly:
		return "  [binding: name-only]"
	}
	return ""
}

// externalRefutationNote marks a refuted row whose refuting evidence was
// SUPPLIED, not executed — a reader weighing a blocked merge must see that
// the counterexample is the supplier's word.
func externalRefutationNote(res schema.ClaimResult) string {
	for _, e := range res.Evidence {
		if e.Refuted() && e.Supplier != "" {
			return "  [external: " + e.Supplier + "]"
		}
	}
	return ""
}

// gateLegs names what blocks the gate on THIS receipt's configuration —
// shared by the renderers so the footer never under-states the gate.
func gateLegs(r schema.Receipt) string {
	legs := []string{"refuted claims"}
	if r.Policy != nil {
		legs = append(legs, "policy misses")
	}
	for _, rec := range r.Intake {
		if rec.Required {
			legs = append(legs, "missing required intake")
			break
		}
	}
	return strings.Join(legs, " and ") + " block"
}

// intakeLine renders one supplier's audit record for the receipt header.
func intakeLine(rec schema.IntakeRecord) string {
	s := fmt.Sprintf("%s (%s ≤%s)", rec.Supplier, rec.Mechanism, rec.MaxTier)
	if !rec.Admitted {
		s += " — not admitted: " + rec.Reason
		if rec.Required {
			s += " — REQUIRED (the gate blocks here)"
		}
		return s
	}
	s += fmt.Sprintf(" — admitted %s, %d row(s) accepted", short(rec.DocDigest), rec.Accepted)
	if n := len(rec.Rejected); n > 0 {
		s += fmt.Sprintf(", %d rejected", n)
	}
	return s
}

// llmEdgeNote discloses why a model-proposed edge did not count: the probe
// passed but the pass raised nothing, either because the coverage gate
// refuted the edge (execution never reached the claim's file) or because no
// profile existed to confirm it. Rendered on remainder rows — the claim's
// probe DID run, and silently rendering the row as "nothing checked" would
// hide that a pass was discarded.
func llmEdgeNote(res schema.ClaimResult) string {
	if res.Claim.Source.Kind != schema.SourceLLM {
		return ""
	}
	for _, e := range res.Evidence {
		if !e.Verified() {
			continue
		}
		switch e.Binding {
		case schema.BindingFileNotReached:
			return "  [llm edge rejected: the probe passed but never executed " + res.Claim.Source.File + "]"
		case "":
			return "  [llm edge unconfirmed: no coverage profile, so the pass raised nothing]"
		}
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

// shaNote renders the immutable pins beside the symbolic refs, abbreviated:
// the commit SHAs, and the input digest that identifies the harvested content
// when the tree carries work no commit SHA covers.
// short abbreviates a hex pin for display; the full value stays in the JSON.
func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func shaNote(c schema.ChangeRef) string {
	var parts []string
	switch {
	case c.BaseSHA != "" && c.HeadSHA != "":
		parts = append(parts, short(c.BaseSHA)+".."+short(c.HeadSHA))
	case c.HeadSHA != "":
		parts = append(parts, "@"+short(c.HeadSHA))
	}
	if c.InputDigest != "" {
		parts = append(parts, "input:"+short(c.InputDigest))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " · ") + ")"
}

// exclusions maps the resolver's scope-exclusion records into the schema.
func exclusions(in []gitdiff.Exclusion) []schema.Exclusion {
	var out []schema.Exclusion
	for _, e := range in {
		out = append(out, schema.Exclusion{Reason: e.Reason, Count: e.Count, Dirs: e.Dirs})
	}
	return out
}

// exclusionNote states the scope boundary's own blind spot in one line —
// shared by every renderer so the disclosure cannot drift between formats.
func exclusionNote(excl []schema.Exclusion) string {
	if len(excl) == 0 {
		return ""
	}
	var parts []string
	for _, e := range excl {
		switch e.Reason {
		case "untracked-territory":
			parts = append(parts, fmt.Sprintf("%d untracked file(s) in never-tracked top-level trees (%s) — invisible until first `git add`",
				e.Count, strings.Join(e.Dirs, ", ")))
		case "untracked-hidden":
			parts = append(parts, fmt.Sprintf("%d hidden untracked file(s)", e.Count))
		default:
			parts = append(parts, fmt.Sprintf("%d file(s): %s", e.Count, e.Reason))
		}
	}
	return "scope excluded " + strings.Join(parts, " · ")
}

// sanitizePaths scrubs machine details from probe detail text. Assemble is
// the CHOKEPOINT: every Evidence.Detail passes through here regardless of
// which runner produced it, so a runner that echoes arbitrary tool output
// (a failing test's own message, a compiler error) cannot leak machine
// layout into a shareable receipt.
//
// The rule is categorical, not an enumerated blocklist: the repository root
// becomes "." (keeping in-repo paths fully readable, relative), and every
// OTHER absolute path collapses to "…/<basename>". Enumerating known-bad
// roots (the old repoRoot + $HOME pair) is incomplete by construction — it
// left temp paths, toolchain roots, other users' homes, and sibling project
// names (a $HOME replacement renders "~/src/<sibling>/…", which still names
// the sibling). An absolute path outside the repo is never the receipt
// reader's business; its basename preserves the actionable part
// ("…/testing.go:1576"). The machine's hostname is scrubbed to "<host>".
// URLs pass untouched: "//" never matches the path shape, and a path
// preceded by an alphanumeric (https:…) is not token-initial.
func sanitizePaths(detail, repoRoot string) string {
	if detail == "" {
		return detail
	}
	if repoRoot != "" && repoRoot != "." {
		detail = strings.ReplaceAll(detail, repoRoot, ".")
	}
	return scrubHost(collapseAbsPaths(detail), hostname)
}

// absPathRe matches a token-initial absolute path of at least two
// components. The colon is a terminator, not a path character, so
// "/a/b/x.go:12: msg" collapses to "…/x.go:12: msg" — the file:line
// actionability survives the redaction.
var absPathRe = regexp.MustCompile("(^|[\\s\"'`=,;(\\[])(/[^/\\x00\\s\"'`:,;)\\]]+(?:/[^/\\x00\\s\"'`:,;)\\]]+)+)")

func collapseAbsPaths(s string) string {
	return absPathRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := absPathRe.FindStringSubmatch(m)
		return sub[1] + "…/" + path.Base(sub[2])
	})
}

// hostname is resolved once; the empty string (lookup failure) disables the
// scrub rather than failing a receipt over it.
var hostname = func() string { h, _ := os.Hostname(); return h }()

// scrubHost replaces whole-word occurrences of the machine's hostname.
// Hostnames shorter than 3 bytes are left alone — a host named "go" would
// otherwise redact ordinary prose, and a leak needs a name distinctive
// enough to identify anything.
func scrubHost(s, host string) string {
	if len(host) < 3 || !strings.Contains(s, host) {
		return s
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(host) + `\b`)
	return re.ReplaceAllString(s, "<host>")
}

// weigh reduces a claim's evidence to a status and an effective tier.
//
//   - refuted:  ANY probe ran and failed. Refutation dominates — a claim bound
//     by five tests where four pass and one fails does NOT hold, and letting
//     passing probes outvote a failing one would launder a defect into a
//     verified row.
//   - verified: no refutation, and some probe ran, passed, and conferred a
//     tier above T0 (Evidence.Verified enforces the tier floor — a T0 pass
//     raises nothing); effective tier is the highest a passing probe
//     conferred.
//   - unverified: nothing ran that could raise the claim. Remainder.
//
// For an LLM-PROPOSED claim, a pass additionally requires a coverage-confirmed
// edge (Evidence.CountsFor — the gate lives in schema so policy evaluation
// applies the identical rule). Fail-closed — a pass with no profile, or one
// whose execution never touched the file, raises nothing and the claim stays
// in the remainder (llmEdgeNote discloses which). Refutation stays
// UNCONDITIONAL: a failing probe in the change blocks the gate no matter
// whose edge bound it.
func weigh(c schema.Claim, evs []schema.Evidence) (schema.Status, schema.Tier) {
	best := schema.T0Unverified
	anyVerified, anyRefuted := false, false
	for _, e := range evs {
		switch {
		case e.CountsFor(c):
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
	fmt.Fprintf(w, "correctful receipt (schema %s%s)\n", r.SchemaVersion, toolNote(r))
	fmt.Fprintf(w, "change: %s...%s%s", r.Change.BaseRef, r.Change.HeadRef, shaNote(r.Change))
	if r.Change.Repo != "" {
		fmt.Fprintf(w, "  [%s]", r.Change.Repo)
	}
	fmt.Fprintf(w, "\nfiles: %d changed\n", len(r.Change.Files))
	if note := exclusionNote(r.Change.Excluded); note != "" {
		fmt.Fprintf(w, "  %s\n", note)
	}
	if p := r.Policy; p != nil {
		fmt.Fprintf(w, "policy: %s · %s · %d rule(s)%s\n", p.Path, short(p.Digest), p.Rules, exemptNote(p))
	}
	for _, rec := range r.Intake {
		fmt.Fprintf(w, "intake: %s\n", intakeLine(rec))
		for _, rej := range rec.Rejected {
			fmt.Fprintf(w, "  rejected: %s %s (%s) — %s\n", rej.ClaimID, rej.ProbeID, rej.Outcome, rej.Reason)
		}
	}
	fmt.Fprintln(w)

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
				fmt.Fprintf(w, "  [%s] %s — %s%s%s%s\n", res.EffectiveTier, res.Claim.ID, res.Claim.Text,
					anchorNote(res.Claim), llmNote(res.Claim), bindingNote(res))
			}
		}
		fmt.Fprintln(w)
	}
	if p := r.Policy; p != nil && len(p.Misses) > 0 {
		fmt.Fprintln(w, "POLICY MISSES (evidence floors not met — the gate blocks here)")
		for _, m := range p.Misses {
			fmt.Fprintf(w, "  %s — %s  [rule: %s]\n", m.File, m.Detail, m.Rule)
		}
		fmt.Fprintln(w)
	}
	if s.Refuted > 0 {
		fmt.Fprintln(w, "REFUTED (a probe ran and the claim did not hold — the gate blocks here)")
		for _, res := range r.Results {
			if res.Status == schema.StatusRefuted {
				fmt.Fprintf(w, "  %s — %s%s\n", res.Claim.ID, detailOf(res), externalRefutationNote(res))
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
		fmt.Fprintf(w, "  %s — %s  [%s:%d]%s%s%s\n", res.Claim.ID, res.Claim.Text,
			res.Claim.Source.File, res.Claim.Source.Line, anchorNote(res.Claim), llmNote(res.Claim), llmEdgeNote(res))
	}

	writeCoverage(w, r.Coverage)
	fmt.Fprintf(w, "\nexit gate: %s; the remainder informs, never fails\n", gateLegs(r))
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
	if hist := unreadHistogram(cov, false); hist != "" {
		fmt.Fprintf(w, "  unread (no harvester for): %s\n", hist)
	}
	if hist := unreadHistogram(cov, true); hist != "" {
		fmt.Fprintf(w, "  unread (policy — hidden paths hold installed tooling): %s\n", hist)
	}
	if cov.SuppressedMentions > 0 {
		fmt.Fprintf(w, "  %s\n", mentionNote(cov.SuppressedMentions))
	}
}

// mentionNote states the premise-gate disclosure identically in every
// renderer — one phrasing, no drift between the receipt's formats.
func mentionNote(n int) string {
	return fmt.Sprintf("%d spec-id mention(s) not minted as claims — the repo defines no spec-id corpus, so a reference has no possible referent", n)
}

// toolNote renders the producing build beside the schema version — shared by
// both renderers so the chain field is visible wherever the receipt is read.
func toolNote(r schema.Receipt) string {
	if r.ToolVersion == "" {
		return ""
	}
	return " · correctful " + r.ToolVersion
}

// exemptNote renders the policy's test-file exemption count when present —
// the exemption is disclosed, never silent.
func exemptNote(p *schema.PolicyResult) string {
	if p.ExemptTestFiles == 0 {
		return ""
	}
	return fmt.Sprintf(" · %d test file(s) exempt (evidence sources)", p.ExemptTestFiles)
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
