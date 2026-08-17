package harvest

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshft/correctful/schema"
)

// Anchoring is the first rung of proof-carrying binding. Every spec-id claim
// correctful mints rests on a name ATTESTATION — a test or a source comment
// says "INV-004" and the claim trusts it. Anchoring asks the cheapest
// mechanical question underneath that trust: does the id even EXIST as a
// stated definition in the repo's document corpus? A claim whose id resolves
// adopts the definition's own words; an id defined nowhere is disclosed as an
// orphan; an id defined differently in several places — id namespaces are
// feature-local in practice, a measured corpus had 14 of 34 spec files each
// defining their own INV-004 — is disclosed as ambiguous and never guessed at.
//
// The corpus deliberately INCLUDES dot-directories, inverting the harvest
// rule. The asymmetry is the point: claims may ORIGINATE only from shipped
// code (a dot-dir carries installed tooling whose ids are not the repo's),
// but an id named by shipped code may RESOLVE against a definition anywhere —
// and the measured repos keep their entire spec corpus under a dot-directory.

// DefSite is one definition heading in the corpus.
type DefSite struct {
	Source schema.Source
	Title  string
}

// DefIndex maps a canonical spec id to every definition site in the corpus.
type DefIndex map[string][]DefSite

// defHeadingRe matches a markdown heading that DEFINES an id: the id token at
// the start, then an explicit separator (colon or dash family) and a title, or
// nothing at all (`### INV-004: Config check routed`, `### INV-018`). The
// token is validated (and canonicalized) by specIDFromSegment afterwards,
// which is what rejects `Inventory-2020` and five-digit lookalikes.
//
// The separator is REQUIRED before a title on purpose: a heading that merely
// STARTS with an id and runs on in plain words — `# AP-031 real-producer
// fixture — stage A`, the measured shape of test-fixture files NAMED for an
// id — describes an artifact about the id, it does not define the id. Every
// true definition in the measured corpora writes the separator.
var defHeadingRe = regexp.MustCompile(`^#{1,6}\s+([A-Za-z]+[-_]?[0-9]{1,5}[a-z]?)(?:\s*[:：—–-]\s*(.*))?$`)

// BuildDefIndex scans document files for definition headings. Files are
// candidates only if they are markdown; fenced code blocks are skipped
// entirely (artifacts quote spec excerpts inside fences as examples — an
// example defines nothing; a verbatim UNFENCED quote of a heading is indexed,
// and resolution's title-grouping keeps it harmless).
func BuildDefIndex(repoDir string, files []string) DefIndex {
	idx := DefIndex{}
	for _, rel := range files {
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".md", ".markdown":
		default:
			continue
		}
		abs := filepath.Join(repoDir, rel)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxScanBytes {
			continue
		}
		content, err := os.ReadFile(abs)
		if err != nil || bytes.IndexByte(content, 0) >= 0 {
			continue
		}
		inFence := false
		for i, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			m := defHeadingRe.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			id := specIDFromSegment(m[1])
			if id == "" {
				continue
			}
			idx[id] = append(idx[id], DefSite{
				Source: schema.Source{
					Kind: schema.SourceSpecDef,
					File: rel,
					Line: i + 1,
					Ref:  m[1],
				},
				Title: strings.TrimSpace(m[2]),
			})
		}
	}
	return idx
}

// AnchorClaims resolves every spec-id claim against the definition index, in
// place, and returns the claims it kept plus a count of suppressed mentions.
//
// PREMISE GATE. With an EMPTY index — a repo that defines no spec-id corpus
// anywhere — a probe-less spec-id reference cannot be a claim: with no
// definition to refer to, the sighting is a MENTION of an identifier, not an
// assertion about a defined invariant. Those claims are dropped and counted
// (the count is disclosed in the receipt's coverage — suppressing remainder
// rows silently would be the exact dishonesty the remainder exists to
// prevent). Everything else — probed spec-id claims, MUST clauses, test-name
// claims — passes through untouched.
//
// The gate is at the PREMISE level deliberately, not the token level.
// Measured across two real corpora (2,880 and 642 sightings): the dominant
// real-assertion convention is a mid-comment parenthetical id — exactly the
// shape of an explanatory example — so no annotation grammar can separate the
// two textually without destroying most of the real harvest. What separates
// them is the referent: real corpora define their ids in spec documents, and
// a repo with zero definitions is not practicing the convention at all. In a
// repo WITH a corpus, unresolvable ids stay as orphans — an orphan against an
// existing vocabulary is an anomaly worth surfacing, and both measured
// corpora yielded true orphans that were real findings.
//
// Two measured refinements beyond exact-id lookup:
//
//   - SUB-VARIANT PARENT FALLBACK. Ids like INV-013d name a clause of
//     INV-013; the measured corpus writes sub-variants only in spec prose,
//     never as their own headings. An id with a sub-variant letter and no
//     heading of its own resolves through its parent's sites — with the
//     parent's collision honestly inherited, and WITHOUT upgrading the
//     claim's text (the parent's title describes the parent, not the clause).
//
//   - CHANGE-SCOPED DISAMBIGUATION. Id namespaces are feature-local, so a
//     bare id is ambiguous corpus-wide — but a change that carries its own
//     spec has declared which namespace its tests speak in. When the
//     colliding sites disagree and EXACTLY ONE definition (one distinct
//     title) lies inside the changed files, that definition is the claim's —
//     a mechanical join, not a guess. A whole-tree sweep changes every file
//     and therefore scopes nothing, which is the correct degeneration.
func AnchorClaims(claims []schema.Claim, idx DefIndex, changed []string) ([]schema.Claim, int) {
	if len(idx) == 0 {
		kept := claims[:0]
		suppressed := 0
		for _, c := range claims {
			if c.Source.Kind == schema.SourceSpecID &&
				specIDFromSegment(c.ID) == c.ID && len(c.ProbeIDs) == 0 {
				suppressed++
				continue
			}
			kept = append(kept, c)
		}
		return kept, suppressed
	}
	inChange := make(map[string]bool, len(changed))
	for _, f := range changed {
		inChange[f] = true
	}
	for i := range claims {
		c := &claims[i]
		if specIDFromSegment(c.ID) != c.ID {
			continue // not a spec-id claim (a MUST clause, a bare test-name claim)
		}
		sites, viaParent := idx[c.ID], false
		if len(sites) == 0 {
			if parent := parentID(c.ID); parent != "" {
				sites, viaParent = idx[parent], true
			}
		}
		if len(sites) == 0 {
			c.Anchor = &schema.Anchor{Status: schema.AnchorOrphan}
			continue
		}
		title, srcs, ok := groupOneTitle(sites)
		if !ok {
			var scoped []DefSite
			for _, s := range sites {
				if inChange[s.Source.File] {
					scoped = append(scoped, s)
				}
			}
			if len(scoped) > 0 {
				title, srcs, ok = groupOneTitle(scoped)
			}
		}
		if !ok {
			_, all, _ := groupOneTitle(sites)
			c.Anchor = &schema.Anchor{Status: schema.AnchorAmbiguous, Sites: all}
			continue
		}
		c.Anchor = &schema.Anchor{Status: schema.AnchorResolved, Title: title, Sites: srcs}
		if title != "" && !viaParent {
			// The definition is authoritative for what the claim SAYS; the
			// test-name provenance survives in Source and the probe ids.
			c.Text = c.ID + ": " + title
		}
	}
	return claims, 0
}

// parentID strips a sub-variant letter: INV-013d -> INV-013. Empty when the
// id has no sub-variant.
func parentID(id string) string {
	last := id[len(id)-1]
	if last >= 'a' && last <= 'z' && len(id) > 1 {
		prev := id[len(id)-2]
		if prev >= '0' && prev <= '9' {
			return id[:len(id)-1]
		}
	}
	return ""
}

// groupOneTitle groups sites by normalized title. ok reports whether they all
// state ONE title — one definition, however many documents quote it. The
// returned sources always cover every given site.
func groupOneTitle(sites []DefSite) (title string, srcs []schema.Source, ok bool) {
	titles := map[string]string{} // normalized -> first original casing
	for _, s := range sites {
		norm := strings.ToLower(strings.Join(strings.Fields(s.Title), " "))
		if _, seen := titles[norm]; !seen {
			titles[norm] = s.Title
		}
		srcs = append(srcs, s.Source)
	}
	if len(titles) != 1 {
		return "", srcs, false
	}
	for _, t := range titles {
		title = t
	}
	return title, srcs, true
}
