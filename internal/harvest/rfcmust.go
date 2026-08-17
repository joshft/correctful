package harvest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshft/correctful/schema"
)

// docExts is the candidate set for the RFC harvester: prose document formats
// that could hold a normative spec. Candidacy only earns a SNIFF — a document
// is harvested when it identifies as normative (see isNormativeDoc), which is
// what keeps this from reopening the extraction-over-prose class the spec-ref
// harvester's code-only allowlist exists to close.
var docExts = map[string]bool{".md": true, ".markdown": true, ".txt": true}

// RFCMustHarvester harvests normative MUST-family clauses from RFC-style
// documents as probe-less must-clause claims — the receipt's honest T0 tenant.
// An RFC that says "the controller MUST durably name both generations" has
// made a claim whether or not anything checks it; a receipt that omits it
// hides the largest part of the remainder.
//
// Scope rules, each measured against a real implementation RFC:
//
//   - UPPERCASE keywords only (MUST, MUST NOT, SHALL, SHALL NOT, REQUIRED).
//     The measured document draws exactly this line itself: 58 uppercase
//     requirement clauses against 4 lowercase "must" sentences that are
//     design-thesis prose, deliberately unmarked. SHOULD/MAY are excluded —
//     the remainder carries obligations, not options.
//   - One claim per SENTENCE, even when the sentence chains two keywords:
//     splitting mid-sentence produces unreadable half-claims.
//   - ```text fences are harvested line-by-line; all other fences are skipped.
//     The measured document keeps its single most load-bearing clause (an
//     authority-subset formula) inside a text fence, while code-labeled fences
//     hold example code whose "MUST" strings are not the document's claims.
//   - The RFC 2119 / BCP 14 interpretation boilerplate is never a claim: it
//     declares the keywords, it doesn't use them.
//
// Clauses carry no probes — and that is a MEASURED state, not a deferral.
// Binding a clause to a test needs a join signal a test can mechanically
// name; measured across every qualifying corpus, the one real RFC's 58
// keyword clauses contain zero spec-ids and zero requirement labels, and a
// fuzzy textual join would mint false bindings (the same class the
// doc-comment binding was cut for). The bindable form is explicit clause
// labels (R-007-style) named by tests — support lands when a corpus adopts
// them. Until then every clause lands in the remainder, which is the point:
// the receipt states the normative surface nothing checks instead of
// silently not knowing it exists.
type RFCMustHarvester struct{}

func (RFCMustHarvester) Name() string { return "rfc-must" }

func (RFCMustHarvester) Harvest(repoDir string, files []string) (Result, error) {
	var res Result
	for _, rel := range files {
		if !docExts[strings.ToLower(filepath.Ext(rel))] {
			continue
		}
		if UnderDotDir(rel) {
			continue // installed tooling and private dot-dirs are not the repo's spec
		}
		abs := filepath.Join(repoDir, rel)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxScanBytes {
			continue
		}
		content, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		// The sniff IS a scan: every candidate opened is reported read, so
		// coverage can honestly say "this document was checked for normative
		// markers and rejected" rather than implying it was never looked at.
		res.Read = append(res.Read, rel)
		if bytes.IndexByte(content, 0) >= 0 {
			continue // binary masquerading as a document
		}
		doc := string(content)
		if !isNormativeDoc(rel, doc) {
			continue
		}
		res.Claims = append(res.Claims, mustClauses(rel, doc)...)
	}
	return res, nil
}

// rfcHeadingRe matches a markdown heading whose title begins with "RFC".
var rfcHeadingRe = regexp.MustCompile(`^#{1,6}\s+RFC\b`)

// isNormativeDoc reports whether a candidate document identifies as a
// normative spec. Three independent markers, any of which qualifies:
//
//   - an "rfc" FILENAME segment (wire-rfc.md, RFC-004.txt) — segment-matched,
//     so "perfconf.md" does not qualify by substring;
//   - a first heading beginning "# RFC" — how the measured document, which
//     carries no 2119 boilerplate at all, actually identifies itself;
//   - RFC 2119 / BCP 14 interpretation boilerplate anywhere in the text.
func isNormativeDoc(rel, content string) bool {
	if hasRFCSegment(rel) {
		return true
	}
	// The boilerplate marker requires the interpretation SENTENCE, not a bare
	// mention: a document that merely talks about "RFC 2119" (this project's
	// own README does) is not thereby declaring its keywords normative.
	// Measured: the mention-based check harvested a README's prose as MUST
	// clauses.
	if strings.Contains(content, "interpreted as described in") &&
		(strings.Contains(content, "RFC 2119") || strings.Contains(content, "BCP 14")) {
		return true
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			return rfcHeadingRe.MatchString(trimmed)
		}
	}
	return false
}

// hasRFCSegment reports whether the filename (extension stripped) contains
// "rfc" as a whole segment under the same segment discipline as spec-id
// detection: split on separators and camel humps, match whole segments only.
func hasRFCSegment(rel string) bool {
	base := filepath.Base(rel)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	for _, seg := range strings.Fields(humanize(b.String())) {
		if strings.EqualFold(seg, "rfc") {
			return true
		}
	}
	return false
}

var (
	// mustKeywordRe is case-SENSITIVE: uppercase is the RFC 2119 convention,
	// and the lowercase "must" of ordinary prose is exactly what this
	// harvester must not mint claims from. Compound phrases come first so the
	// full phrase is the match.
	mustKeywordRe = regexp.MustCompile(`\b(?:MUST NOT|SHALL NOT|MUST|SHALL|REQUIRED)\b`)
	bulletRe      = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	fenceRe       = regexp.MustCompile("^(?:```|~~~)\\s*(.*)$")
)

// docFrag is one physical line's contribution to a logical unit of prose.
type docFrag struct {
	text string
	line int
}

// mustClauses extracts one claim per keyword-bearing sentence. The document is
// first gathered into logical UNITS — a paragraph, a list item, a table row, a
// text-fence line — so a clause wrapped across physical lines is one sentence,
// then each unit is sentence-split.
func mustClauses(rel, content string) []schema.Claim {
	var claims []schema.Claim
	seq := 0
	var unit []docFrag

	flush := func() {
		if len(unit) != 0 {
			claims = append(claims, unitClaims(rel, unit, &seq)...)
			unit = nil
		}
	}

	inFence, fenceLabel := false, ""
	for i, raw := range strings.Split(content, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		// Blockquote markers are structure, not content: the measured document
		// sets a key invariant in a bold blockquote, and leaving the "> " in
		// would leak markers into the middle of the joined sentence.
		for strings.HasPrefix(trimmed, ">") {
			trimmed = strings.TrimSpace(trimmed[1:])
		}
		if m := fenceRe.FindStringSubmatch(trimmed); m != nil {
			flush()
			if inFence {
				inFence, fenceLabel = false, ""
			} else {
				inFence, fenceLabel = true, strings.ToLower(strings.TrimSpace(m[1]))
			}
			continue
		}
		if inFence {
			if fenceLabel == "text" && trimmed != "" {
				claims = append(claims, unitClaims(rel, []docFrag{{trimmed, lineNo}}, &seq)...)
			}
			continue
		}
		switch {
		case trimmed == "":
			flush()
		case strings.HasPrefix(trimmed, "#"):
			flush() // headings title sections; they are not clauses
		case strings.HasPrefix(trimmed, "|"):
			flush()
			claims = append(claims, unitClaims(rel, []docFrag{{trimmed, lineNo}}, &seq)...)
		case bulletRe.MatchString(raw):
			flush()
			unit = append(unit, docFrag{strings.TrimSpace(bulletRe.ReplaceAllString(raw, "")), lineNo})
		default:
			unit = append(unit, docFrag{trimmed, lineNo})
		}
	}
	flush()
	return claims
}

// unitClaims joins a unit's fragments, splits it into sentences, and mints one
// claim per keyword-bearing sentence. Line provenance points at the physical
// line where the sentence BEGINS, resolved through the joined text's offsets.
func unitClaims(rel string, unit []docFrag, seq *int) []schema.Claim {
	type fragOff struct{ off, line int }
	var b strings.Builder
	offs := make([]fragOff, 0, len(unit))
	for _, f := range unit {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		offs = append(offs, fragOff{b.Len(), f.line})
		b.WriteString(f.text)
	}
	text := b.String()

	var claims []schema.Claim
	for _, span := range sentenceSpans(text) {
		sent := strings.TrimSpace(text[span[0]:span[1]])
		kw := mustKeywordRe.FindString(sent)
		if kw == "" {
			continue
		}
		if strings.Contains(sent, "RFC 2119") || strings.Contains(sent, "BCP 14") {
			continue // the interpretation boilerplate declares keywords; it makes no requirement
		}
		line := 0
		for _, fo := range offs {
			if fo.off <= span[0] {
				line = fo.line
			}
		}
		*seq++
		claims = append(claims, schema.Claim{
			ID:    fmt.Sprintf("MUST:%s:%d", rel, *seq),
			Shape: schema.ShapeMustClause,
			Text:  sent,
			Source: schema.Source{
				Kind: schema.SourceRFCMust,
				File: rel,
				Line: line,
				Ref:  kw,
			},
			// No probes: a MUST clause is remainder-bound in v0 by design.
		})
	}
	return claims
}

// sentenceSpans splits joined unit text into sentence spans. A boundary is
// terminal punctuation (optionally followed by closing quotes/brackets), then
// whitespace, then an uppercase letter or digit — which keeps "v0.2" and
// similar dotted tokens whole. A unit without terminal punctuation (a fence
// formula, a table row) is one sentence.
func sentenceSpans(s string) [][2]int {
	var spans [][2]int
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '.', '!', '?':
		default:
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == '"' || s[j] == '\'' || s[j] == ')' || s[j] == ']' || s[j] == '`') {
			j++
		}
		if j >= len(s) || s[j] != ' ' {
			continue
		}
		k := j
		for k < len(s) && s[k] == ' ' {
			k++
		}
		// A new sentence may open with inline punctuation before its first
		// letter — a backtick code span ("`UNTESTED` is not…"), a quote, bold
		// markers — so look through opening punctuation for the uppercase test
		// while keeping the sentence's start at the punctuation itself.
		next := k
		for next < len(s) && (s[next] == '"' || s[next] == '\'' || s[next] == '`' || s[next] == '*' || s[next] == '(' || s[next] == '[') {
			next++
		}
		if next < len(s) && ((s[next] >= 'A' && s[next] <= 'Z') || (s[next] >= '0' && s[next] <= '9')) {
			spans = append(spans, [2]int{start, j})
			start = k
			i = k - 1
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		spans = append(spans, [2]int{start, len(s)})
	}
	return spans
}
