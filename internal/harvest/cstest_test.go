package harvest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joshft/correctful/schema"
)

// TestCSharpClassIDsRecognized: the C# naming convention carries ids in the
// CLASS name, in mixed case, sometimes several, sometimes with an "And"
// continuation. All shapes observed in real methodology-following repos
// (identifiers anonymized per AGENTS.md).
func TestCSharpClassIDsRecognized(t *testing.T) {
	cases := map[string][]string{
		"Inv018AcceptancePolicyTests":    {"INV-018"},
		"Inv010Inv011ParityHarnessTests": {"INV-010", "INV-011"},
		"Inv009And010LoaderTests":        {"INV-009", "INV-010"},
		"GateBoundaryTb007Tests":         {"TB-007"},
		"CodeownersCoverageTests":        nil,
		"Band010Tests":                   nil, // "Band010" is one segment; matches nothing
	}
	for name, want := range cases {
		got := specIDsInName(name)
		if len(got) != len(want) {
			t.Errorf("specIDsInName(%q) = %v, want %v", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("specIDsInName(%q)[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

// realShapedFixture is shaped verbatim after a real methodology-following xUnit
// test file (captured 2026-08-16) — per the real-fixture rule, a parser of
// another project's artifacts must be tested against that artifact's actual
// SHAPE. The structure is preserved exactly: file-scoped namespace, XML doc
// comment cross-referencing spec ids, attribute placement, [Theory] with
// [InlineData], snake_case method names, per-method "Tests INV-nnn" comments.
// All domain identifiers are anonymized per AGENTS.md.
const realShapedFixture = `using System.Linq;
using Example.Gate;
using Xunit;

namespace Example.Gate.Tests;

/// <summary>
/// Acceptance spec INV-018 (~628-663), TB-006 — the PINNED acceptance policy
/// <see cref="AcceptancePolicy.Pinned"/> exercised against the REAL committed tree
/// (integration, ` + "`git ls-files`" + `). Documents + guards the maintainer-selected "narrow roots +
/// anchors" boundary.
/// </summary>
public class Inv018AcceptancePolicyTests
{
    private static readonly System.Collections.Generic.IReadOnlyList<string> RepoTree =
        ManifestProducer.EnumerateRepoFiles(TestPaths.RepoRoot());

    // Tests INV-018 [integration]: the pinned policy is well-shaped (roots end in '/', exact anchors,
    // exact exclusions, no globs).
    [Fact]
    public void Pinned_policy_validates()
    {
        PolicyValidation v = AcceptancePolicy.Pinned.Validate();
        Assert.True(v.Valid, $"pinned policy must validate; reason: '{v.Reason}'");
    }

    // Tests INV-018 [integration]: known in-scope files (committed) ARE in the accepted set.
    [Theory]
    [InlineData("scripts/run-checks.sh")]
    [InlineData("gate/Example.Core/Loader.cs")]
    public void In_scope_file_is_in_the_accepted_set(string path)
    {
        var accepted = Classifier.DiscoverAcceptedSet(AcceptancePolicy.Pinned, RepoTree);
        Assert.Contains(path, accepted);
    }
}
`

// TestCSharpHarvesterVerbatimFixture: against the real file shape, every
// [Fact]/[Theory] method claims the class's invariant, bound to a dotnet-test
// probe scoped to the containing csproj. The doc comment naming TB-006 must
// NOT mint a claim — comments are not coverage signals.
func TestCSharpHarvesterVerbatimFixture(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "gate", "Example.Gate.Tests")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"Example.Gate.Tests.csproj":      "<Project/>",
		"Inv018AcceptancePolicyTests.cs": realShapedFixture,
	} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	harvested, err := CSharpTestHarvester{}.Harvest(dir, []string{"gate/Example.Gate.Tests/Inv018AcceptancePolicyTests.cs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(harvested.Claims) != 2 {
		t.Fatalf("claims = %d (%v), want 2", len(harvested.Claims), harvested.Claims)
	}
	wantProbe := map[string]string{
		"Pinned_policy_validates":              schema.DotnetTestProbeID("gate/Example.Gate.Tests/Example.Gate.Tests.csproj", "Inv018AcceptancePolicyTests.Pinned_policy_validates"),
		"In_scope_file_is_in_the_accepted_set": schema.DotnetTestProbeID("gate/Example.Gate.Tests/Example.Gate.Tests.csproj", "Inv018AcceptancePolicyTests.In_scope_file_is_in_the_accepted_set"),
	}
	seen := map[string]bool{}
	for _, c := range harvested.Claims {
		if c.ID != "INV-018" {
			t.Errorf("claim id = %q, want INV-018 (TB-006 from the doc comment must not mint)", c.ID)
		}
		if c.Shape != schema.ShapeInvariant {
			t.Errorf("shape = %q, want invariant", c.Shape)
		}
		method := c.Source.Ref[len("Inv018AcceptancePolicyTests."):]
		if len(c.ProbeIDs) != 1 || c.ProbeIDs[0] != wantProbe[method] {
			t.Errorf("probe for %s = %v, want %q", method, c.ProbeIDs, wantProbe[method])
		}
		seen[method] = true
	}
	if !seen["Pinned_policy_validates"] || !seen["In_scope_file_is_in_the_accepted_set"] {
		t.Errorf("methods harvested = %v", seen)
	}
}

// TestCSharpHarvesterSkipsCommentedTests: a [Fact] inside a line comment or a
// block comment must not mint a claim.
func TestCSharpHarvesterSkipsCommentedTests(t *testing.T) {
	src := `namespace X;
public class MiscTests
{
    // [Fact]
    // public void Commented_out_test() { }
    /* [Fact]
    public void Block_commented_test() { } */
    [Fact]
    public void Real_test() { }
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "P.csproj"), []byte("<Project/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MiscTests.cs"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	harvested, err := CSharpTestHarvester{}.Harvest(dir, []string{"MiscTests.cs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(harvested.Claims) != 1 || harvested.Claims[0].Source.Ref != "MiscTests.Real_test" {
		t.Fatalf("claims = %v, want only MiscTests.Real_test", harvested.Claims)
	}
}

// TestCSharpMethodIDOverridesClassID: a method whose own name carries an id
// claims that id, not its class's.
func TestCSharpMethodIDOverridesClassID(t *testing.T) {
	src := `public class Inv018MixedTests
{
    [Fact]
    public void Prh004_guard_holds() { }
}
`
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "P.csproj"), []byte("<Project/>"), 0o644)
	os.WriteFile(filepath.Join(dir, "T.cs"), []byte(src), 0o644)
	harvested, err := CSharpTestHarvester{}.Harvest(dir, []string{"T.cs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(harvested.Claims) != 1 || harvested.Claims[0].ID != "PRH-004" {
		t.Fatalf("claims = %v, want one PRH-004 claim", harvested.Claims)
	}
}
