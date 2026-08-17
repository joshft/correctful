package harvest

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshft/correctful/schema"
)

// codeExts is the allowlist of extensions the spec-ref harvester will scan.
//
// A spec identifier is a claim the change MAKES only when it appears in code the
// change ships — code that asserts, by naming the invariant, that it upholds it.
// An identifier listed in a prose catalog (ARCHITECTURE.md, an index, a journal)
// is documentation about claims, not a claim; scanning those files reproduces
// the extraction-over-prose class (whole categories of false remainder entries).
// Purpose-built harvesters handle the structured non-code sources — Alloy asserts
// and RFC MUST clauses — each parsing its artifact rather than scraping tokens.
var codeExts = map[string]bool{
	".go": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".cs": true, ".java": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".rs": true, ".py": true, ".rb": true, ".sh": true, ".bash": true, ".kt": true,
	".swift": true, ".m": true, ".mm": true, ".php": true, ".scala": true,
	".ex": true, ".exs": true, ".erl": true, ".lua": true, ".pl": true, ".sql": true,
}

func isCodeFile(path string) bool {
	return codeExts[strings.ToLower(filepath.Ext(path))]
}

// isTestPath reports whether a file is test code or test data by Go
// convention (_test.go, testdata/). Other languages' conventions are added as
// dogfooding measures their noise, not preemptively.
func isTestPath(rel string) bool {
	if strings.HasSuffix(rel, "_test.go") {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "testdata" {
			return true
		}
	}
	return false
}

// UnderDotDir reports whether any component of the path is hidden. Hidden
// directories hold vendored tooling and configuration (.correctless, .claude,
// .github), not code the project ships — a repo with the methodology's tooling
// INSTALLED would otherwise have the tooling's own identifiers harvested as the
// project's claims. Measured on a real sweep: every one of 75 remainder entries
// came from installed tooling under a dot-directory, zero from project code.
func UnderDotDir(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// maxScanBytes bounds how much of a file the reference scanner reads. Spec
// identifiers live near where code is written, not megabytes deep; capping the
// scan keeps a stray large or binary file from stalling a receipt.
const maxScanBytes = 2 << 20 // 2 MiB

// SpecRefHarvester scans changed files for methodology identifiers (INV/BND/PRH/
// ABS/PAT/TB/AP) and proposes an invariant claim for each distinct one — with NO
// bound probe. Reconciliation in Run() promotes any such claim that also has a
// harvested test to a verifiable claim; the ones left unmatched are exactly the
// receipt's remainder: invariants the change names but nothing checks.
type SpecRefHarvester struct{}

func (SpecRefHarvester) Name() string { return "spec-ref" }

func (SpecRefHarvester) Harvest(repoDir string, files []string) (Result, error) {
	sites := map[string][]schema.Source{}
	var order []string // first-seen id order for stable output
	var res Result

	for _, rel := range files {
		if !isCodeFile(rel) {
			continue // prose/catalog files list claims; they don't make them
		}
		if UnderDotDir(rel) {
			continue // installed tooling makes its own claims, not this repo's
		}
		if isTestPath(rel) {
			// A test file naming an id is either a name-bound test (the test
			// harvesters mint that claim properly) or fixture content — not a
			// SHIPPED-code claim. Measured on a self-sweep: scanning test
			// files filled the remainder with fixture identifiers, which
			// weakens the remainder's credibility without adding a claim.
			continue
		}
		abs := filepath.Join(repoDir, rel)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() == 0 {
			continue
		}
		f, err := os.Open(abs)
		if err != nil {
			continue
		}
		// Read here means identifier-scanned only — a coverage entry whose sole
		// reader is spec-ref was never parsed for native claim constructs.
		res.Read = append(res.Read, rel)
		scanRefs(f, rel, sites, &order)
		f.Close()
	}

	// One claim per id, carrying EVERY reference site: the sites are the
	// annotated code regions a coverage-proven binding later checks probes
	// against, so dropping all but the first sighting would blind the prover.
	for _, id := range order {
		res.Claims = append(res.Claims, schema.Claim{
			ID:       id,
			Shape:    schema.ShapeInvariant,
			Text:     id + " (referenced; no bound probe from harvest)",
			Source:   sites[id][0],
			RefSites: sites[id],
			// No probes: this claim is remainder-bound unless a test for the
			// same id is harvested and merges its probes in reconciliation.
		})
	}
	return res, nil
}

// scanRefs records every identifier sighting in one file — at most one site
// per (id, line) — into sites/order.
func scanRefs(f *os.File, rel string, sites map[string][]schema.Source, order *[]string) {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	lineNo := 0
	var read int
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		read += len(line)
		if read > maxScanBytes {
			return
		}
		if bytes.IndexByte(line, 0) >= 0 {
			return // binary file: stop scanning it entirely
		}
		for _, m := range specIDRe.FindAll(line, -1) {
			id := normalizeSpecID(string(m))
			if id == "" {
				continue
			}
			if _, seen := sites[id]; !seen {
				*order = append(*order, id)
			} else if last := sites[id][len(sites[id])-1]; last.File == rel && last.Line == lineNo {
				continue // same id twice on one line is one site
			}
			sites[id] = append(sites[id], schema.Source{
				Kind: schema.SourceSpecID,
				File: rel,
				Line: lineNo,
				Ref:  string(m),
			})
		}
	}
}
