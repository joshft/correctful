package harvest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshft/correctful/schema"
)

// CSharpTestHarvester reads changed C# files and proposes one claim per
// [Fact]/[Theory] test method, bound to a dotnet-test probe.
//
// The id-carrying convention differs from Go: in xUnit codebases following the
// methodology, the invariant id lives in the CLASS name (Inv018PinnedPolicyTests,
// Inv010Inv011Layer2RealCosignTests) and the methods under it are snake_case
// assertions of that invariant. So every test method of an id-carrying class
// claims that class's id(s) — the merge step then unions them into one claim
// with a probe per method, all of which run and any of which can refute. A
// method whose own name carries an id overrides its class; a method in an
// id-less class is a plain assertion claim.
//
// Parsing is a comment-aware line scanner, not an AST: a stdlib-grade C# parser
// is not available to a pure-Go binary, and a tree-sitter sidecar is a later
// increment. The scanner's failure direction is benign by construction: a
// phantom method (mis-scan) mints a probe whose filter matches nothing, which
// the runner records as Ran=false — never as a pass.
type CSharpTestHarvester struct{}

func (CSharpTestHarvester) Name() string { return "cs-test" }

func (CSharpTestHarvester) Harvest(repoDir string, files []string) (Result, error) {
	var res Result
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".cs") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoDir, rel))
		if err != nil {
			continue
		}
		csproj := findCsproj(repoDir, rel)
		if csproj == "" {
			continue // no containing project: nothing could ever run the probe
		}
		res.Read = append(res.Read, rel)
		res.Claims = append(res.Claims, scanCSharpTests(string(src), rel, csproj)...)
	}
	return res, nil
}

var (
	csClassRe  = regexp.MustCompile(`^\s*(?:(?:public|internal|private|protected|sealed|static|partial|abstract|file)\s+)*class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	csAttrRe   = regexp.MustCompile(`^\[\s*(Fact|Theory)\b`)
	csMethodRe = regexp.MustCompile(`^\s*(?:(?:public|internal|private|protected|static|async|virtual|override)\s+)+(?:void|Task(?:<[^>]*>)?|ValueTask)\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func scanCSharpTests(src, rel, csproj string) []schema.Claim {
	var claims []schema.Claim
	currentClass := ""
	var classIDs []string
	pendingTest := false
	inBlock := false

	for lineNo, raw := range strings.Split(src, "\n") {
		line := raw
		if inBlock {
			end := strings.Index(line, "*/")
			if end < 0 {
				continue
			}
			inBlock = false
			line = line[end+2:]
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if start := strings.Index(line, "/*"); start >= 0 && !strings.Contains(line[start:], "*/") {
			inBlock = true
			line = line[:start]
			trimmed = strings.TrimSpace(line)
		}
		if trimmed == "" {
			continue
		}

		if m := csClassRe.FindStringSubmatch(line); m != nil {
			currentClass = m[1]
			classIDs = specIDsInName(m[1])
			pendingTest = false
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if csAttrRe.MatchString(trimmed) {
				pendingTest = true
			}
			// Other attributes ([InlineData], [Trait]) leave pending as-is.
			continue
		}
		if m := csMethodRe.FindStringSubmatch(line); m != nil && pendingTest && currentClass != "" {
			pendingTest = false
			claims = append(claims, csClaims(currentClass, m[1], classIDs, rel, csproj, lineNo+1)...)
		}
	}
	return claims
}

// csClaims builds the claim(s) for one test method: its own name's ids win,
// else the class's ids, else a plain assertion.
func csClaims(class, method string, classIDs []string, rel, csproj string, line int) []schema.Claim {
	probe := schema.DotnetTestProbeID(csproj, class+"."+method)
	source := schema.Source{
		Kind: schema.SourceDotnetTest,
		File: rel, Line: line, Ref: class + "." + method,
	}

	ids := specIDsInName(method)
	if len(ids) == 0 {
		ids = classIDs
	}
	if len(ids) == 0 {
		return []schema.Claim{{
			ID:       class + "." + method,
			Shape:    schema.ShapeAssertion,
			Text:     humanize(method),
			Source:   source,
			ProbeIDs: []string{probe},
		}}
	}
	claims := make([]schema.Claim, 0, len(ids))
	for _, id := range ids {
		claims = append(claims, schema.Claim{
			ID:       id,
			Shape:    schema.ShapeInvariant,
			Text:     humanize(method),
			Source:   source,
			ProbeIDs: []string{probe},
		})
	}
	return claims
}

// findCsproj walks up from the file's directory to the repo root looking for a
// .csproj, returning its repo-relative path ("" if none).
func findCsproj(repoDir, rel string) string {
	dir := filepath.Dir(rel)
	for {
		matches, _ := filepath.Glob(filepath.Join(repoDir, dir, "*.csproj"))
		if len(matches) > 0 {
			if r, err := filepath.Rel(repoDir, matches[0]); err == nil {
				return r
			}
		}
		if dir == "." || dir == "/" {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}
