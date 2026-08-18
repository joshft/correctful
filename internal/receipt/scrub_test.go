package receipt

import (
	"strings"
	"testing"
	"unicode"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/schema"
)

// hostileReceipt seeds every renderer-visible string with a terminal escape
// and a Markdown-structural break: an ESC + clear-screen, newlines, a
// heading, a code-span-breaking backtick, and a table-breaking pipe.
func hostileReceipt() schema.Receipt {
	bad := "\x1b[2J\n\n## Forged gate pass\n`backtick`|pipe"
	claims := []schema.Claim{{
		ID: "A1" + bad, Shape: schema.ShapeAssertion, Text: "text" + bad,
		Source:   schema.Source{File: "f.go" + bad, Line: 1, Ref: "r" + bad},
		ProbeIDs: []string{"p" + bad},
	}, {
		ID: "A2" + bad, Shape: schema.ShapeAssertion, Text: "unverified" + bad,
		Source: schema.Source{File: "g.go" + bad},
	}}
	evidence := [][]schema.Evidence{
		{{ClaimID: claims[0].ID, ProbeID: "px" + bad, Tier: schema.T1Assertion, Ran: true, Passed: true, Detail: "d" + bad, Mechanism: "m" + bad}},
		{{ClaimID: claims[1].ID, Ran: false}},
	}
	cov := schema.Coverage{
		Files:   []schema.FileCoverage{{File: "f.go" + bad, ReadBy: []string{"h" + bad}, Claims: 1}, {File: "g.go" + bad, ReadBy: []string{"h"}}},
		Claimed: 1, Scanned: 1,
	}
	r := Assemble(gitdiff.Change{
		Repo: "repo" + bad, BaseRef: "base" + bad, HeadRef: "head" + bad,
		Files: []string{"f.go" + bad, "g.go" + bad},
	}, claims, evidence, cov)
	r.ToolVersion = "v" + bad
	r.Policy = &schema.PolicyResult{
		Path: "correctful.json" + bad, Digest: strings.Repeat("a", 64), Rules: 1,
		Misses: []schema.PolicyMiss{{File: "f.go" + bad, Rule: "rule" + bad, Detail: "detail" + bad}},
	}
	r.Intake = []schema.IntakeRecord{{
		Supplier: "s" + bad, Mechanism: "proof" + bad, MaxTier: schema.T4Mechanical, Admitted: true, Accepted: 1,
		Rejected: []schema.IntakeRejection{{ClaimID: "c" + bad, ProbeID: "p" + bad, Outcome: "error" + bad, Reason: "why" + bad}},
	}}
	r.Signature = &schema.SignatureBlock{Alg: "ed25519", PublicKey: "k" + bad, Audience: "aud" + bad, Sig: "sig" + bad}
	return r
}

// TestRenderersStripControlRunes: neither renderer may emit a control rune,
// no matter what a hostile receipt carries. A rendering reaches a terminal
// or a PR comment, and render does not verify its input.
func TestRenderersStripControlRunes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		render func() string
	}{
		{"text", func() string { var b strings.Builder; WriteText(&b, hostileReceipt()); return b.String() }},
		{"markdown", func() string { var b strings.Builder; WriteMarkdown(&b, hostileReceipt()); return b.String() }},
	} {
		out := tc.render()
		for i, r := range out {
			// The renderers emit newlines for their own structure; those
			// are the only control runes allowed, and only because they
			// come from the renderer, never from a field (fields have
			// their newlines stripped).
			if r == '\n' {
				continue
			}
			if unicode.IsControl(r) {
				t.Fatalf("%s: control rune %#U at offset %d", tc.name, r, i)
			}
		}
	}
}

// TestMarkdownRejectsInjectedHeading: the forged "## Forged gate pass"
// heading must never appear at the start of a line — that is how a pasted
// receipt would fake an authoritative section.
func TestMarkdownRejectsInjectedHeading(t *testing.T) {
	var b strings.Builder
	WriteMarkdown(&b, hostileReceipt())
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "## Forged") {
			t.Fatalf("injected heading reached a line start: %q", line)
		}
	}
}
