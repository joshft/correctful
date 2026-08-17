package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

// The corpus fixture mirrors the measured shape of a real methodology repo
// (captured 2026-08-17, identifiers anonymized per AGENTS.md): definition
// headings are `### <ID>: <title>`; the spec corpus lives under a DOT
// directory (.correctless/specs plus repo-global catalogs); id namespaces are
// FEATURE-LOCAL, so two spec files define the same INV id with different
// titles; review artifacts QUOTE spec headings verbatim; and code fences can
// contain heading-shaped examples that define nothing.
func writeAnchorCorpus(t *testing.T) (dir string, files []string) {
	t.Helper()
	corpus := map[string]string{
		".correctless/specs/loader.md": `# Spec: Loader

### INV-001: The loader accepts only signed bundles

- Enforcement: signature table test.

### INV-004: Config check is routed through the loader

Body text referencing INV-001 inline (a mention, not a definition).
`,
		".correctless/specs/relay.md": `# Spec: Relay

### INV-004: Frames forward in arrival order

A different feature restarts its id numbering — the collision is real.
`,
		".correctless/antipatterns.md": `# Antipatterns

### AP-007: Config parsed but never wired

Frequency: 2 features.
`,
		// A review artifact quoting the loader spec's heading VERBATIM: same
		// title, so INV-001 still names exactly one invariant.
		".correctless/artifacts/review-loader.md": `# Review findings

Quoted for context:

### INV-001: The loader accepts only signed bundles

The finding text.
`,
		"docs/design.md": "# Design\n\n```\n### INV-002: heading inside a fence defines nothing\n```\n\n### Inventory-2020 report\n\n### INV-00423: not an id (five digits)\n",
		// A test fixture NAMED for an id — heading starts with the id but runs
		// on in plain words with no separator. Describes an artifact about the
		// id; defines nothing (the measured false-definition shape).
		"gate/fixtures/adr-block.md": "# AP-007 real-producer fixture stage A\n",
	}
	dir = t.TempDir()
	for rel, content := range corpus {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	return dir, files
}

// TestBuildDefIndexShapes: the index holds every definition heading — dot-dir
// documents INCLUDED, because that is where the spec corpus lives (the
// asymmetry with claim harvesting is deliberate: ids ORIGINATE only from
// shipped code, but they may RESOLVE against documents anywhere) — while
// fenced examples and heading-shaped decoys define nothing.
func TestBuildDefIndexShapes(t *testing.T) {
	dir, files := writeAnchorCorpus(t)
	idx := BuildDefIndex(dir, files)

	if n := len(idx["INV-001"]); n != 2 {
		t.Errorf("INV-001 sites = %d, want 2 (spec + verbatim quote in artifact)", n)
	}
	if n := len(idx["INV-004"]); n != 2 {
		t.Errorf("INV-004 sites = %d, want 2 (feature-local collision)", n)
	}
	if n := len(idx["AP-007"]); n != 1 {
		t.Errorf("AP-007 sites = %d, want 1 (catalog only — the fixture heading NAMED for it defines nothing)", n)
	}
	for _, absent := range []string{"INV-002", "INV-00423"} {
		if _, ok := idx[absent]; ok {
			t.Errorf("%s indexed — fenced examples and >4-digit tokens define nothing", absent)
		}
	}
	if site := idx["AP-007"][0]; site.Source.Kind != schema.SourceSpecDef || site.Source.File != ".correctless/antipatterns.md" || site.Title != "Config parsed but never wired" {
		t.Errorf("AP-007 site = %+v", site)
	}
}

// TestAnchorClaims: resolution outcomes against the measured corpus shapes —
// a uniquely-titled id RESOLVES and upgrades the claim's text to the
// definition's words; a feature-local collision is AMBIGUOUS (disclosed,
// never guessed); an id the corpus does not define is an ORPHAN; claims whose
// id is not a spec identifier are untouched.
func TestAnchorClaims(t *testing.T) {
	dir, files := writeAnchorCorpus(t)
	idx := BuildDefIndex(dir, files)

	claims := []schema.Claim{
		{ID: "INV-001", Shape: schema.ShapeInvariant, Text: "INV-001: loader accepts signed bundles (humanized test name)"},
		{ID: "INV-004", Shape: schema.ShapeInvariant, Text: "INV-004 (referenced; no bound probe from harvest)"},
		{ID: "INV-777", Shape: schema.ShapeInvariant, Text: "INV-777 (referenced; no bound probe from harvest)"},
		{ID: "MUST:docs/wire-rfc.md:1", Shape: schema.ShapeMustClause, Text: "The relay MUST forward frames in order."},
	}
	AnchorClaims(claims, idx, nil)

	resolved := claims[0]
	if resolved.Anchor == nil || resolved.Anchor.Status != schema.AnchorResolved {
		t.Fatalf("INV-001 anchor = %+v, want resolved", resolved.Anchor)
	}
	if resolved.Text != "INV-001: The loader accepts only signed bundles" {
		t.Errorf("INV-001 text = %q — a resolved claim adopts the definition's words", resolved.Text)
	}
	if resolved.Anchor.Title != "The loader accepts only signed bundles" || len(resolved.Anchor.Sites) != 2 {
		t.Errorf("INV-001 anchor detail = %+v", resolved.Anchor)
	}

	ambiguous := claims[1]
	if ambiguous.Anchor == nil || ambiguous.Anchor.Status != schema.AnchorAmbiguous {
		t.Fatalf("INV-004 anchor = %+v, want ambiguous", ambiguous.Anchor)
	}
	if !strings.Contains(ambiguous.Text, "referenced") {
		t.Errorf("INV-004 text = %q — an ambiguous claim must NOT adopt either definition", ambiguous.Text)
	}
	if len(ambiguous.Anchor.Sites) != 2 || ambiguous.Anchor.Title != "" {
		t.Errorf("INV-004 anchor detail = %+v, want both colliding sites and no title", ambiguous.Anchor)
	}

	orphan := claims[2]
	if orphan.Anchor == nil || orphan.Anchor.Status != schema.AnchorOrphan {
		t.Fatalf("INV-777 anchor = %+v, want orphan", orphan.Anchor)
	}

	if claims[3].Anchor != nil {
		t.Errorf("MUST-clause claim got anchor %+v — anchoring is for spec-id claims only", claims[3].Anchor)
	}
}

// TestAnchorClaimsNoCorpus: a repo with no definition corpus gets no anchor
// annotations at all — a repo that never states invariants in documents
// should not have every claim flagged for it.
func TestAnchorClaimsNoCorpus(t *testing.T) {
	claims := []schema.Claim{{ID: "INV-001", Shape: schema.ShapeInvariant, Text: "t"}}
	AnchorClaims(claims, DefIndex{}, nil)
	if claims[0].Anchor != nil {
		t.Errorf("anchor = %+v, want nil when the corpus is empty", claims[0].Anchor)
	}
}

// TestAnchorSubVariantFallsBackToParent: INV-013d has no heading of its own —
// the measured corpus writes sub-variants only in spec prose — so it resolves
// through INV-013's sites, inheriting the parent's collision honestly instead
// of reporting "defined nowhere". A sub-variant WITH its own heading uses it.
func TestAnchorSubVariantFallsBackToParent(t *testing.T) {
	dir := t.TempDir()
	corpus := map[string]string{
		"specs/loader.md": "### INV-013: Loader bounds startup reads\n\n### INV-020a: Rendezvous marker is single-writer\n",
		"specs/relay.md":  "### INV-013: Relay bounds frame reads\n",
	}
	var files []string
	for rel, content := range corpus {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	idx := BuildDefIndex(dir, files)

	claims := []schema.Claim{
		{ID: "INV-013d", Shape: schema.ShapeInvariant, Text: "orig-13d"},
		{ID: "INV-020a", Shape: schema.ShapeInvariant, Text: "orig-20a"},
		{ID: "INV-099z", Shape: schema.ShapeInvariant, Text: "orig-99z"},
	}
	AnchorClaims(claims, idx, nil)

	if a := claims[0].Anchor; a == nil || a.Status != schema.AnchorAmbiguous || len(a.Sites) != 2 {
		t.Errorf("INV-013d anchor = %+v, want ambiguous via parent's two colliding sites", claims[0].Anchor)
	}
	if a := claims[1].Anchor; a == nil || a.Status != schema.AnchorResolved || claims[1].Text != "INV-020a: Rendezvous marker is single-writer" {
		t.Errorf("INV-020a = %+v text %q, want own-heading resolution with text upgrade", claims[1].Anchor, claims[1].Text)
	}
	if a := claims[2].Anchor; a == nil || a.Status != schema.AnchorOrphan {
		t.Errorf("INV-099z anchor = %+v, want orphan (no own heading, no parent)", claims[2].Anchor)
	}
}

// TestAnchorChangeScopedDisambiguation: a corpus-wide collision resolves when
// exactly one definition lies inside the CHANGED files — the change carrying
// its own spec has declared which namespace its tests speak in. Sub-variants
// resolving via a change-scoped parent gain the status but NOT a text upgrade
// (the parent's title describes the parent, not the clause). Without the
// change scope both stay ambiguous.
func TestAnchorChangeScopedDisambiguation(t *testing.T) {
	dir := t.TempDir()
	corpus := map[string]string{
		"specs/loader.md": "### INV-004: Config check is routed through the loader\n",
		"specs/relay.md":  "### INV-004: Frames forward in arrival order\n",
	}
	var files []string
	for rel, content := range corpus {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	idx := BuildDefIndex(dir, files)

	fresh := func() []schema.Claim {
		return []schema.Claim{
			{ID: "INV-004", Shape: schema.ShapeInvariant, Text: "orig-4"},
			{ID: "INV-004b", Shape: schema.ShapeInvariant, Text: "orig-4b"},
		}
	}

	unscoped := fresh()
	AnchorClaims(unscoped, idx, nil)
	for _, c := range unscoped {
		if c.Anchor == nil || c.Anchor.Status != schema.AnchorAmbiguous {
			t.Errorf("%s without change scope = %+v, want ambiguous", c.ID, c.Anchor)
		}
	}

	scoped := fresh()
	AnchorClaims(scoped, idx, []string{"specs/loader.md", "gate/loader_test.go"})
	own := scoped[0]
	if own.Anchor == nil || own.Anchor.Status != schema.AnchorResolved ||
		own.Text != "INV-004: Config check is routed through the loader" ||
		len(own.Anchor.Sites) != 1 || own.Anchor.Sites[0].File != "specs/loader.md" {
		t.Errorf("INV-004 change-scoped = %+v text %q", own.Anchor, own.Text)
	}
	sub := scoped[1]
	if sub.Anchor == nil || sub.Anchor.Status != schema.AnchorResolved || sub.Text != "orig-4b" {
		t.Errorf("INV-004b change-scoped = %+v text %q, want resolved via parent WITHOUT text upgrade", sub.Anchor, sub.Text)
	}
}
