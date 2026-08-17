package receipt

import (
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/schema"
)

// TestMarkdownRemainderNeverCollapses: in the PR comment, the unverified
// remainder must be visible without a click — folding it away would defeat the
// receipt's whole purpose — while the verified list may collapse.
func TestMarkdownRemainderNeverCollapses(t *testing.T) {
	claims, evidence := sampleClaims()
	r := Assemble(gitdiff.Change{BaseRef: "main", HeadRef: "pr"}, claims, evidence, schema.Coverage{})
	var b strings.Builder
	WriteMarkdown(&b, r)
	out := b.String()

	if !strings.HasPrefix(out, MarkdownMarker) {
		t.Error("markdown receipt must start with the update marker")
	}
	remIdx := strings.Index(out, "Unverified remainder")
	if remIdx < 0 {
		t.Fatal("remainder section missing")
	}
	// The remainder section must not sit inside a <details> fold.
	if open := strings.Index(out, "<details>"); open >= 0 && open < remIdx {
		if close := strings.Index(out, "</details>"); close > remIdx {
			t.Error("remainder is folded inside <details>")
		}
	}
	if !strings.Contains(out, "<details><summary>✅ Verified") {
		t.Error("verified list should be collapsible")
	}
	if !strings.Contains(out, "Refuted") {
		t.Error("refuted section missing for a receipt with a refuted claim")
	}
	if !strings.Contains(out, "Harvest coverage:") {
		t.Error("coverage disclosure missing from markdown receipt")
	}
}

// TestMarkdownEmptyRemainderIsStated: an empty remainder renders as an explicit
// statement, not an absent section.
func TestMarkdownEmptyRemainderIsStated(t *testing.T) {
	r := Assemble(gitdiff.Change{}, nil, nil, schema.Coverage{})
	var b strings.Builder
	WriteMarkdown(&b, r)
	if !strings.Contains(b.String(), "Empty — every harvested claim reached a probe") {
		t.Error("empty remainder not explicitly stated")
	}
}

// TestMarkdownEscapesTableCells: a claim detail containing a pipe must not
// break the table it renders into.
func TestMarkdownEscapesTableCells(t *testing.T) {
	if got := mdCell("a|b\nc"); got != `a\|b c` {
		t.Errorf("mdCell = %q", got)
	}
}
