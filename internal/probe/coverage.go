package probe

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/joshft/correctful/schema"
)

// Coverage-proven binding — the second rung of proof-carrying binding.
//
// A go-test probe bound to a claim by its NAME is an attestation. When the
// claim's id is also written into shipped code (a RefSite), the probe's own
// execution can be checked against that annotation mechanically: instrument
// the probe run with a coverage profile and ask whether execution reached the
// enclosing function of any reference site. "covered" upgrades the trust
// story from "the test says it is about INV-004" to "the test demonstrably
// executes the code region annotated with INV-004" — still not proof that it
// asserts the right property, and the receipt never claims more.
//
// The mechanics live behind two package-level maps because execution is
// DEDUPED per probe while binding is evaluated per (claim, probe) EDGE: the
// dispatcher marks which probes warrant instrumentation before the run
// (coverWanted), the go-test runner stores each instrumented run's parsed
// profile (covProfiles), and the dispatcher's attribution pass evaluates
// every edge's own reference sites against the one shared profile.
//
// Instrumentation must never degrade a verdict: the runner falls back to an
// uninstrumented run when the instrumented one could not execute, and a probe
// with no profile simply carries no binding statement.

var (
	coverWanted sync.Map // probeID -> true, set by Dispatch before running
	covProfiles sync.Map // probeID -> covProfile, set by GoTestRunner
)

// covBlock is one profile entry: a statement block and its execution count.
type covBlock struct {
	startLine, endLine int
	count              int
}

// covProfile maps an import-path-qualified file to its blocks.
type covProfile map[string][]covBlock

// parseCoverProfile reads the `go test -coverprofile` format:
//
//	example.com/mod/pkg/file.go:18.65,19.20 1 1
func parseCoverProfile(data string) covProfile {
	prof := covProfile{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		colon := strings.LastIndexByte(line, ':')
		if colon < 0 {
			continue
		}
		file := line[:colon]
		var span, stmts, count string
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			continue
		}
		span, stmts, count = fields[0], fields[1], fields[2]
		_ = stmts
		start, end, ok := strings.Cut(span, ",")
		if !ok {
			continue
		}
		sl, err1 := strconv.Atoi(strings.SplitN(start, ".", 2)[0])
		el, err2 := strconv.Atoi(strings.SplitN(end, ".", 2)[0])
		n, err3 := strconv.Atoi(count)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		prof[file] = append(prof[file], covBlock{startLine: sl, endLine: el, count: n})
	}
	return prof
}

// funcSpan is one function's line extent: doc comment through body end. The
// doc comment is included because the measured annotation style writes the id
// either inside the body or in the comment directly above the function.
type funcSpan struct {
	docStart, bodyStart, bodyEnd int
}

var funcSpanCache sync.Map // abs file path -> []funcSpan (nil on parse failure)

// funcSpans parses a Go file once and returns its function extents.
func funcSpans(absFile string) []funcSpan {
	if v, ok := funcSpanCache.Load(absFile); ok {
		spans, _ := v.([]funcSpan)
		return spans
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absFile, nil, parser.ParseComments)
	var spans []funcSpan
	if err == nil {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			s := funcSpan{
				docStart:  fset.Position(fd.Pos()).Line,
				bodyStart: fset.Position(fd.Body.Pos()).Line,
				bodyEnd:   fset.Position(fd.Body.End()).Line,
			}
			if fd.Doc != nil {
				s.docStart = fset.Position(fd.Doc.Pos()).Line
			}
			spans = append(spans, s)
		}
	}
	funcSpanCache.Store(absFile, spans)
	return spans
}

// bindingFor evaluates one claim's reference sites against a probe's profile:
// "covered" when execution reached the enclosing function of ANY site,
// "name-only" when at least one site was checkable and none was reached, ""
// when nothing was checkable (no .go site inside a function the profile
// knows). Sites in test files are ignored — a test naming the id is the
// attestation being checked, not evidence for it.
func bindingFor(repoDir string, refSites []schema.Source, prof covProfile) string {
	checkable := false
	for _, site := range refSites {
		if !strings.HasSuffix(site.File, ".go") || strings.HasSuffix(site.File, "_test.go") {
			continue
		}
		blocks := profileBlocksFor(prof, site.File)
		if blocks == nil {
			continue
		}
		var enclosing *funcSpan
		for _, s := range funcSpans(filepath.Join(repoDir, site.File)) {
			if site.Line >= s.docStart && site.Line <= s.bodyEnd {
				enclosing = &s
				break
			}
		}
		if enclosing == nil {
			continue // annotation on a const/type/file comment: not checkable
		}
		checkable = true
		for _, b := range blocks {
			if b.count > 0 && b.startLine <= enclosing.bodyEnd && b.endLine >= enclosing.bodyStart {
				return schema.BindingCovered
			}
		}
	}
	if checkable {
		return schema.BindingNameOnly
	}
	return ""
}

// fileBindingFor evaluates a model-proposed edge at file granularity: did the
// probe's execution reach the claim's file at all? Weaker than the
// function-level check — an LLM claim carries a file, never a line — but
// mechanical, and it is the gate that lets a model-proposed edge count:
// "file-covered" when any block of the file executed, "file-not-reached"
// when the instrumented run never touched it (including a file the profile
// does not know, which with -coverpkg=./... means it was not built in).
func fileBindingFor(prof covProfile, relFile string) string {
	for _, b := range profileBlocksFor(prof, relFile) {
		if b.count > 0 {
			return schema.BindingFileCovered
		}
	}
	return schema.BindingFileNotReached
}

// scopeOf reduces a profile to the probe's measured execution footprint: the
// number of distinct package directories holding an EXECUTED block. One
// directory is a single-package run; more is cross-package — the axis a
// policy floor needs to require integration-shaped evidence. A profile with
// no executed blocks stays unmeasured rather than guessing.
func scopeOf(prof covProfile) string {
	dirs := map[string]bool{}
	for f, blocks := range prof {
		for _, b := range blocks {
			if b.count > 0 {
				dirs[path.Dir(f)] = true
				break
			}
		}
	}
	switch len(dirs) {
	case 0:
		return ""
	case 1:
		return schema.ScopeSinglePackage
	default:
		return schema.ScopeCrossPackage
	}
}

// profileBlocksFor finds the profile entry whose import-qualified path ends
// with the repo-relative file, on a path-component boundary.
func profileBlocksFor(prof covProfile, relFile string) []covBlock {
	for f, blocks := range prof {
		if f == relFile || strings.HasSuffix(f, "/"+relFile) {
			return blocks
		}
	}
	return nil
}

// hasGoRefSite reports whether a claim carries a reference site the Go
// coverage prover could check.
func hasGoRefSite(c schema.Claim) bool {
	for _, s := range c.RefSites {
		if strings.HasSuffix(s.File, ".go") && !strings.HasSuffix(s.File, "_test.go") {
			return true
		}
	}
	return false
}
