package receipt

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/joshft/correctful/schema"
)

// MarkdownMarker is the first line of every markdown receipt. A CI workflow
// finds its previous comment by this marker and updates it in place instead of
// stacking a new comment per push.
const MarkdownMarker = "<!-- correctful-receipt -->"

// WriteMarkdown renders the receipt as a pull-request comment. The ordering is
// the product's opinion: refutations come first and loud, the unverified
// remainder is ALWAYS visible and never collapsed, the verified list folds away
// (it is the part reviewers already trust), and coverage closes the comment so
// "all verified" can never be read apart from "out of how much".
func WriteMarkdown(w io.Writer, r schema.Receipt) {
	s := r.Summary
	fmt.Fprintln(w, MarkdownMarker)
	fmt.Fprintf(w, "## correctful receipt\n\n")
	fmt.Fprintf(w, "**%d claims** — ✅ %d verified · ❌ %d refuted · ⚠️ %d unverified\n\n",
		s.TotalClaims, s.Verified, s.Refuted, s.Unverified)
	if a := s.Anchoring; a != nil {
		fmt.Fprintf(w, "Anchoring: %d of %d spec-id claims resolved to definitions · %d ambiguous · %d orphan\n\n",
			a.Resolved, a.SpecIDClaims, a.Ambiguous, a.Orphan)
	}
	fmt.Fprintf(w, "Change: `%s...%s` — %d files\n\n", r.Change.BaseRef, r.Change.HeadRef, len(r.Change.Files))

	if s.Refuted > 0 {
		fmt.Fprintln(w, "### ❌ Refuted — a probe ran and the claim did not hold")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Claim | What failed |")
		fmt.Fprintln(w, "|---|---|")
		for _, res := range r.Results {
			if res.Status == schema.StatusRefuted {
				fmt.Fprintf(w, "| `%s` | %s |\n", mdCell(res.Claim.ID), mdCell(detailOf(res)))
			}
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "### ⚠️ Unverified remainder (%d) — what nothing checked\n\n", len(r.Remainder))
	if len(r.Remainder) == 0 {
		fmt.Fprintln(w, "_Empty — every harvested claim reached a probe._")
	} else {
		fmt.Fprintln(w, "| Claim | Statement | Source |")
		fmt.Fprintln(w, "|---|---|---|")
		for _, res := range r.Remainder {
			fmt.Fprintf(w, "| `%s` | %s | `%s:%d` |\n",
				mdCell(res.Claim.ID), mdCell(res.Claim.Text+anchorNote(res.Claim)),
				res.Claim.Source.File, res.Claim.Source.Line)
		}
	}
	fmt.Fprintln(w)

	if s.Verified > 0 {
		fmt.Fprintf(w, "<details><summary>✅ Verified (%d)</summary>\n\n", s.Verified)
		fmt.Fprintln(w, "| Tier | Claim | Statement |")
		fmt.Fprintln(w, "|---|---|---|")
		for _, res := range r.Results {
			if res.Status == schema.StatusVerified {
				fmt.Fprintf(w, "| %s | `%s` | %s |\n",
					res.EffectiveTier, mdCell(res.Claim.ID), mdCell(res.Claim.Text+anchorNote(res.Claim)))
			}
		}
		fmt.Fprintln(w, "\n</details>")
		fmt.Fprintln(w)
	}

	cov := r.Coverage
	fmt.Fprintf(w, "**Harvest coverage:** %d files — %d claimed · %d scanned · %d unread\n",
		len(cov.Files), cov.Claimed, cov.Scanned, cov.Unread)
	if hist := unreadHistogram(cov); hist != "" {
		fmt.Fprintf(w, "<sub>unread (no harvester for): %s</sub>\n", hist)
	}
	fmt.Fprintf(w, "\n<sub>schema %s · exit gate: refuted claims block; the remainder informs, never fails</sub>\n", r.SchemaVersion)
}

// mdCell makes text safe inside a markdown table cell.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

// unreadHistogram renders the unread files grouped by extension, most common
// first — shared shape with the text renderer's disclosure.
func unreadHistogram(cov schema.Coverage) string {
	counts := map[string]int{}
	for _, f := range cov.Files {
		if len(f.ReadBy) == 0 && f.Claims == 0 {
			ext := path.Ext(f.File)
			if ext == "" {
				ext = "(none)"
			}
			counts[ext]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	exts := make([]string, 0, len(counts))
	for e := range counts {
		exts = append(exts, e)
	}
	sort.Slice(exts, func(i, j int) bool {
		if counts[exts[i]] != counts[exts[j]] {
			return counts[exts[i]] > counts[exts[j]]
		}
		return exts[i] < exts[j]
	})
	parts := make([]string, 0, len(exts))
	for _, e := range exts {
		parts = append(parts, fmt.Sprintf("%s×%d", e, counts[e]))
	}
	return strings.Join(parts, " ")
}
