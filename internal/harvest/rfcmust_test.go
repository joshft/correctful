package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

// realRFCFixture is shaped verbatim after a real implementation-RFC markdown
// document (captured 2026-08-17) — the `# RFC:` title, the bold metadata block,
// a terminology bullet whose MUST clause wraps across lines, a numbered design
// thesis that uses lowercase "must" deliberately, prose paragraphs with wrapped
// uppercase clauses, a ```text fence holding a normative formula with no
// trailing period, a table, a bold BLOCKQUOTE invariant whose "> " markers
// must not leak into claim text, and a follow-on sentence that opens with
// inline punctuation rather than an uppercase letter. All domain identifiers
// and sentences are anonymized per AGENTS.md; the language-labeled fence decoy
// is a synthetic addition, because the real document's fences are all
// text-labeled.
//
// Backtick fences cannot appear literally inside a Go raw string, so the
// fixture writes each fence as three apostrophes and substitutes real
// backticks below.
var realRFCFixture = strings.ReplaceAll(`# RFC: Example Attenuation Runtime
## Externally Imposed Narrowing for Running Workloads

**Status:** Draft v0.2 - implementation / experiment RFC
**Platform:** Linux, stock-kernel first

## 1. Terminology

- **Domain:** Tasks governed as one principal; a managed subtree
  in v0.2.
- **Incarnation:** Backend identity distinguishing separate
  enrollments that may reuse the same kernel ID. Retained
  domain-owned labels MUST bind to an incarnation, not only a
  recyclable numeric ID.

## 2. Design Thesis

1. Narrowing is a transition between states.
2. Narrowing must be externally imposed.
3. Narrowing must not depend on relocation.

The runtime MUST NOT depend on a particular detector, telemetry
system, or application framework. New coverage work MUST extend the
declared registry rather than fork it.

## 3. Authority Model

'''text
Authority(narrowed) MUST be a subset of Authority(normal)
'''

| Mechanism | Normal | Narrowed |
| --- | --- | --- |
| attach | allowed | denied |

An operator lowering the floor must be prevented from acquiring new
authority. Results MUST state how the evidence identity was bound to
those artifacts and MUST name the omitted classes. Enrolled
workloads MUST apply an enrollment floor rather than claim post-hoc
revocation, and MUST report unresolved classes. The floor MUST NOT
prevent relinquishing authority.

> **Retirement MUST precede reclamation, and enrollment MUST fail
> closed if identity cannot be established.**

Each manifest MUST contain exactly one explicit status for every
claim. 'UNTESTED' is not implicit support, and one backend MUST NOT
cite another backend's evidence.

'''go
// synthetic decoy: a keyword inside example code is not a clause
log.Fatal("callers MUST NOT retry")
'''
`, "'''", "```")

// TestRFCMustVerbatimFixture: against the real document shape, every uppercase
// MUST-family sentence mints exactly one probe-less must-clause claim — one per
// SENTENCE even when the sentence carries two keywords — while lowercase
// "must", headings, tables without keywords, and language-labeled fences mint
// nothing. The ```text fence's formula IS harvested: the real document keeps
// its most load-bearing clause there.
func TestRFCMustVerbatimFixture(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "example-rfc-v0.2.md"), []byte(realRFCFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := RFCMustHarvester{}.Harvest(dir, []string{"example-rfc-v0.2.md"})
	if err != nil {
		t.Fatal(err)
	}

	wantTexts := []string{
		"Retained domain-owned labels MUST bind to an incarnation, not only a recyclable numeric ID.",
		"The runtime MUST NOT depend on a particular detector, telemetry system, or application framework.",
		"New coverage work MUST extend the declared registry rather than fork it.",
		"Authority(narrowed) MUST be a subset of Authority(normal)",
		"Results MUST state how the evidence identity was bound to those artifacts and MUST name the omitted classes.",
		"Enrolled workloads MUST apply an enrollment floor rather than claim post-hoc revocation, and MUST report unresolved classes.",
		"The floor MUST NOT prevent relinquishing authority.",
		"**Retirement MUST precede reclamation, and enrollment MUST fail closed if identity cannot be established.**",
		"Each manifest MUST contain exactly one explicit status for every claim.",
		"'UNTESTED' is not implicit support, and one backend MUST NOT cite another backend's evidence.",
	}
	if len(res.Claims) != len(wantTexts) {
		var got []string
		for _, c := range res.Claims {
			got = append(got, c.Text)
		}
		t.Fatalf("claims = %d, want %d\ngot:\n  %s", len(res.Claims), len(wantTexts), strings.Join(got, "\n  "))
	}
	for i, want := range wantTexts {
		c := res.Claims[i]
		if c.Text != want {
			t.Errorf("claim %d text = %q, want %q", i, c.Text, want)
		}
		if c.Shape != schema.ShapeMustClause {
			t.Errorf("claim %d shape = %q, want must-clause", i, c.Shape)
		}
		if c.Source.Kind != schema.SourceRFCMust {
			t.Errorf("claim %d source kind = %q, want rfc-must", i, c.Source.Kind)
		}
		if len(c.ProbeIDs) != 0 {
			t.Errorf("claim %d has probes %v — a MUST clause is remainder-bound in v0", i, c.ProbeIDs)
		}
	}

	// Identity: minted ids are unique, file-scoped, and sequence-ordered.
	if res.Claims[0].ID != "MUST:example-rfc-v0.2.md:1" || res.Claims[9].ID != "MUST:example-rfc-v0.2.md:10" {
		t.Errorf("minted ids = %q .. %q, want MUST:<file>:1 .. :10", res.Claims[0].ID, res.Claims[9].ID)
	}

	// Provenance: Ref is the matched keyword phrase; Line locates the sentence.
	if res.Claims[1].Source.Ref != "MUST NOT" {
		t.Errorf("claim 1 ref = %q, want MUST NOT (full phrase, not bare MUST)", res.Claims[1].Source.Ref)
	}
	if res.Claims[0].Source.Ref != "MUST" {
		t.Errorf("claim 0 ref = %q, want MUST", res.Claims[0].Source.Ref)
	}
	// Line pins where the SENTENCE begins, so a clause is locatable even when
	// its keyword sits on a later wrapped line: the bullet's "Retained
	// domain-owned labels MUST bind..." sentence begins mid-line on the
	// "kernel ID. Retained" line, one line above its keyword.
	if got, want := res.Claims[3].Source.Line, lineOf(t, realRFCFixture, "Authority(narrowed) MUST"); got != want {
		t.Errorf("fence formula line = %d, want %d", got, want)
	}
	if got, want := res.Claims[0].Source.Line, lineOf(t, realRFCFixture, "enrollments that may reuse"); got != want {
		t.Errorf("bullet clause line = %d, want %d (the sentence-start line)", got, want)
	}
}

// lineOf returns the 1-based line number of the first line containing sub.
func lineOf(t *testing.T, doc, sub string) int {
	t.Helper()
	for i, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, sub) {
			return i + 1
		}
	}
	t.Fatalf("fixture has no line containing %q", sub)
	return 0
}

// TestRFCMustQualification: only documents that identify as normative are
// harvested — an "rfc" filename segment, a first heading beginning "# RFC", or
// RFC 2119 / BCP 14 boilerplate. Every candidate document is still READ (the
// sniff is the scan), so coverage reports it honestly; non-qualifying and
// dot-dir files mint nothing.
func TestRFCMustQualification(t *testing.T) {
	files := map[string]string{
		// Qualifies by filename segment.
		"docs/wire-rfc.md": "The relay MUST forward frames in order.\n",
		// Qualifies by first heading.
		"design.md": "# RFC: Design Notes\n\nThe loader MUST reject unsigned bundles.\n",
		// Qualifies by 2119 boilerplate — and the boilerplate sentence itself
		// must NOT mint a claim.
		"conventions.txt": "The key words \"MUST\" and \"MUST NOT\" are to be interpreted as described in RFC 2119.\n\nClients MUST close idle sessions.\n",
		// "rfc" as a substring of one segment is NOT a segment match.
		"perfconf.md": "Benchmarks MUST NOT be committed.\n",
		// Plain notes: read, sniffed, rejected.
		"notes.md": "The gate MUST hold under load.\n",
		// Dot-dir: not read at all.
		".notes/private-rfc.md": "Secrets MUST NOT leak.\n",
	}
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var rels []string
	for rel := range files {
		rels = append(rels, rel)
	}
	res, err := RFCMustHarvester{}.Harvest(dir, rels)
	if err != nil {
		t.Fatal(err)
	}

	claimsByFile := map[string][]schema.Claim{}
	for _, c := range res.Claims {
		claimsByFile[c.Source.File] = append(claimsByFile[c.Source.File], c)
	}
	for file, want := range map[string]int{
		"docs/wire-rfc.md":      1,
		"design.md":             1,
		"conventions.txt":       1,
		"perfconf.md":           0,
		"notes.md":              0,
		".notes/private-rfc.md": 0,
	} {
		if got := len(claimsByFile[file]); got != want {
			t.Errorf("%s: %d claims, want %d (%v)", file, got, want, claimsByFile[file])
		}
	}
	if cs := claimsByFile["conventions.txt"]; len(cs) == 1 && cs[0].Text != "Clients MUST close idle sessions." {
		t.Errorf("boilerplate leak: conventions.txt claim = %q", cs[0].Text)
	}

	read := map[string]bool{}
	for _, f := range res.Read {
		read[f] = true
	}
	for _, f := range []string{"docs/wire-rfc.md", "design.md", "conventions.txt", "perfconf.md", "notes.md"} {
		if !read[f] {
			t.Errorf("%s not reported read — the sniff IS a scan and coverage must say so", f)
		}
	}
	if read[".notes/private-rfc.md"] {
		t.Error("dot-dir document was read; installed tooling and private notes are out of scope")
	}
}

// TestRFCMustDefaultRegistration: the default harvester set includes rfc-must,
// so a receipt over a repo with an RFC gets its clauses in the remainder
// without any configuration.
func TestRFCMustDefaultRegistration(t *testing.T) {
	for _, h := range Default() {
		if h.Name() == "rfc-must" {
			return
		}
	}
	t.Error("Default() does not include the rfc-must harvester")
}

// TestSentenceSpansOpeningPunctuation: a sentence that opens with inline
// punctuation — a backtick code span, a quote, bold markers — is still a new
// sentence. The measured document writes "…for every claim. `UNTESTED` is not
// implicit support…", and requiring a bare uppercase letter after the period
// fused such pairs into one claim.
func TestSentenceSpansOpeningPunctuation(t *testing.T) {
	s := "Each manifest MUST contain one status for every claim. `UNTESTED` is not implicit support, and one backend MUST NOT cite another backend's evidence."
	spans := sentenceSpans(s)
	if len(spans) != 2 {
		t.Fatalf("sentenceSpans split into %d, want 2: %q", len(spans), s)
	}
	second := s[spans[1][0]:spans[1][1]]
	if !strings.HasPrefix(second, "`UNTESTED`") {
		t.Errorf("second sentence = %q, want it to begin at the backtick", second)
	}
}
