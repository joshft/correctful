package harvest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/joshft/correctful/schema"
)

// GoTestHarvester reads changed `*_test.go` files and proposes claims bound to
// the tests that verify them. Each claim is born with a probe — the test — so it
// can reach a mechanical tier rather than sitting in the remainder.
//
// It binds an invariant to a test through ONE structural signal: the invariant
// id is a segment of the test NAME. Naming a test for an invariant is a
// deliberate coverage claim by the author; a MENTION of an id anywhere else
// (a doc comment, a body comment) is not. An earlier version also bound ids
// named in doc comments — measured against a real production change, that bound
// antipatterns and cross-referenced invariants that were only discussed,
// producing false "verified" results. A false pass betrays the one promise a
// checker makes, so only the high-precision name signal survives; everything
// else stays honestly in the remainder.
//
// Parsing is go/ast, not text: correctful is a correctness tool, and a harvester
// that mis-read a build-tagged or commented-out function would poison the
// receipt at its source.
type GoTestHarvester struct{}

func (GoTestHarvester) Name() string { return "go-test" }

func (GoTestHarvester) Harvest(repoDir string, files []string) (Result, error) {
	var res Result
	fset := token.NewFileSet()

	for _, rel := range files {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		abs := filepath.Join(repoDir, rel)
		f, err := parser.ParseFile(fset, abs, nil, 0)
		if err != nil {
			// A file that no longer parses (deleted, or mid-edit) is skipped,
			// not fatal — and NOT reported as read, so coverage shows it.
			continue
		}
		res.Read = append(res.Read, rel)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			if !isTestFunc(name) {
				continue
			}
			line := fset.Position(fn.Pos()).Line
			primary := claimFromTestName(name, rel, line)
			res.Claims = append(res.Claims, primary)

			for _, id := range extraBoundIDs(name, primary.ID) {
				res.Claims = append(res.Claims, schema.Claim{
					ID:    id,
					Shape: schema.ShapeInvariant,
					Text:  id + " (reference-bound by " + name + ")",
					Source: schema.Source{
						Kind: schema.SourceGoTest,
						File: rel, Line: line, Ref: name,
					},
					ProbeIDs: []string{schema.GoTestProbeID(rel, name)},
				})
			}
		}
	}
	return res, nil
}

// isTestFunc reports whether name is a Go test entry point we harvest. We take
// TestXxx but not the TestMain harness and not bare "Test".
func isTestFunc(name string) bool {
	if name == "TestMain" || len(name) <= len("Test") {
		return false
	}
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	next := name[len("Test")]
	return next == '_' || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9')
}

// claimFromTestName derives the primary claim from a test function name. If the
// name contains a spec identifier segment, the claim is that invariant; else it
// is a plain assertion identified by the test name itself.
func claimFromTestName(name, file string, line int) schema.Claim {
	body := testBody(name)
	ids := specIDsInName(body)

	shape := schema.ShapeAssertion
	id := name
	if len(ids) > 0 {
		shape = schema.ShapeInvariant
		id = ids[0]
	}
	return schema.Claim{
		ID:    id,
		Shape: shape,
		Text:  testClaimText(body),
		Source: schema.Source{
			Kind: schema.SourceGoTest,
			File: file, Line: line, Ref: name,
		},
		ProbeIDs: []string{schema.GoTestProbeID(file, name)},
	}
}

// extraBoundIDs returns additional invariant ids a test binds beyond the
// primary — the case of a name that encodes more than one id
// (TestINV001_And_BND002_…). primaryID is excluded. Only name segments count;
// see the type doc for why doc-comment mentions are not bound.
func extraBoundIDs(name, primaryID string) []string {
	seen := map[string]bool{primaryID: true}
	var out []string
	for _, id := range specIDsInName(testBody(name)) {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// testBody strips the Test prefix (and a leading underscore) from a test name.
func testBody(name string) string {
	return strings.TrimPrefix(strings.TrimPrefix(name, "Test"), "_")
}

// testClaimText renders the human statement of a test-derived claim, dropping
// any segment that is itself a spec id.
func testClaimText(body string) string {
	var keep []string
	for _, seg := range strings.Fields(humanize(body)) {
		if specIDFromSegment(seg) == "" {
			keep = append(keep, seg)
		}
	}
	return strings.Join(keep, " ")
}
