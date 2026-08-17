package harvest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshft/correctful/schema"
)

// AlloyHarvester reads changed Alloy (.als) model files and proposes:
//
//   - one safety-assert claim per `assert Name { … }`, bound to an alloy-check
//     probe iff the file also declares `check Name …`. An assert with NO check
//     command lands in the remainder — a property written down and never
//     tested, the formal-methods remainder in its purest form.
//
//   - one witness claim per `run Name …` command. A run witness demands the
//     model ADMIT the scenario: the vacuity guard. Sixteen passing safety
//     checks mean nothing if the model is inconsistent and admits no traces —
//     so the witness is a first-class claim, with pass semantics inverted in
//     the runner (a solution must EXIST).
type AlloyHarvester struct{}

func (AlloyHarvester) Name() string { return "alloy" }

var (
	alsAssertRe = regexp.MustCompile(`^\s*assert\s+([A-Za-z_][A-Za-z0-9_'"]*)`)
	alsCheckRe  = regexp.MustCompile(`^\s*check\s+([A-Za-z_][A-Za-z0-9_'"]*)`)
	alsRunRe    = regexp.MustCompile(`^\s*run\s+([A-Za-z_][A-Za-z0-9_'"]*)`)
)

func (AlloyHarvester) Harvest(repoDir string, files []string) (Result, error) {
	var res Result
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".als") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoDir, rel))
		if err != nil {
			continue
		}
		res.Read = append(res.Read, rel)
		res.Claims = append(res.Claims, scanAlloy(string(src), rel)...)
	}
	return res, nil
}

type alsDecl struct {
	name string
	line int
}

func scanAlloy(src, rel string) []schema.Claim {
	var asserts, runs []alsDecl
	checked := map[string]bool{}
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
		// Alloy has both C-style and Lisp-heritage line comments.
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "--") {
			continue
		}
		if start := strings.Index(line, "/*"); start >= 0 && !strings.Contains(line[start:], "*/") {
			inBlock = true
			line = line[:start]
		}

		switch {
		case alsAssertRe.MatchString(line):
			asserts = append(asserts, alsDecl{alsAssertRe.FindStringSubmatch(line)[1], lineNo + 1})
		case alsCheckRe.MatchString(line):
			checked[alsCheckRe.FindStringSubmatch(line)[1]] = true
		case alsRunRe.MatchString(line):
			runs = append(runs, alsDecl{alsRunRe.FindStringSubmatch(line)[1], lineNo + 1})
		}
	}

	claims := make([]schema.Claim, 0, len(asserts)+len(runs))
	for _, a := range asserts {
		c := schema.Claim{
			ID:    a.name,
			Shape: schema.ShapeSafetyAssert,
			Text:  humanize(a.name),
			Source: schema.Source{
				Kind: schema.SourceAlloyAssert,
				File: rel, Line: a.line, Ref: a.name,
			},
		}
		if checked[a.name] {
			c.ProbeIDs = []string{schema.AlloyCheckProbeID(rel, a.name)}
		}
		// No check command: no probe — the assert is remainder-bound, which is
		// exactly the honest reading of "asserted but never checked".
		claims = append(claims, c)
	}
	for _, r := range runs {
		claims = append(claims, schema.Claim{
			ID:    r.name,
			Shape: schema.ShapeWitness,
			Text:  humanize(r.name) + " (model admits this scenario)",
			Source: schema.Source{
				Kind: schema.SourceAlloyRun,
				File: rel, Line: r.line, Ref: r.name,
			},
			ProbeIDs: []string{schema.AlloyCheckProbeID(rel, r.name)},
		})
	}
	return claims
}
