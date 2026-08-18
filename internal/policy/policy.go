// Package policy evaluates a repository's declared evidence floors against
// an assembled receipt — the "repository policy evaluates the receipt" leg
// of the end state.
//
// A policy is opt-in per path: a rule names the paths it governs and the
// floor their evidence must meet (a minimum tier, optionally a required
// mechanism and a required execution scope). Evaluation is per changed
// file: every matched non-test file must have at least one verified claim
// that TIES to it and whose evidence meets the floor. A tie is structural,
// never assumed — the claim's own source file, or a reference site in
// shipped code. A file nothing demonstrably ties evidence to is a miss
// stated exactly that way; policy floors are therefore only useful where
// claims tie to code (spec-id annotations, or LLM claims with
// coverage-confirmed edges), which is honest: an untied floor SHOULD fail.
//
// A missing policy file means no policy — nothing required, nothing missed.
// A malformed policy file is a loud error, never a silent allow: a broken
// floor must not fail open.
package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joshft/correctful/schema"
)

// File is the policy's well-known repo-relative location. Deliberately NOT
// under a hidden directory: the policy is part of the repository's trust
// base and deserves the same visibility as the code it governs.
const File = "correctful.json"

// Rule is one evidence floor over a set of paths.
type Rule struct {
	// Name labels the rule in misses; when empty, the paths label it.
	Name string `json:"name,omitempty"`
	// Paths the rule governs: an exact repo-relative path, a "dir/..."
	// subtree, or a path.Match glob (whose * does not cross a slash).
	Paths []string `json:"paths"`
	// MinTier is the floor: the tier the tied evidence must reach (1–4).
	MinTier int `json:"min_tier"`
	// Mechanism, when set, requires the tied evidence's probe kind
	// (e.g. "go-test-pair" for an adversarial floor).
	Mechanism string `json:"mechanism,omitempty"`
	// Scope, when set, requires the tied evidence's measured execution
	// footprint (e.g. "cross-package" for an integration floor). Only
	// instrumented runs carry a scope, so a scope floor demands one.
	Scope string `json:"scope,omitempty"`
}

// Policy is a parsed, validated policy file.
type Policy struct {
	PolicyVersion int    `json:"policy_version"`
	Rules         []Rule `json:"rules"`

	digest string
}

// mechanismRe is the token shape a rule's mechanism must have. The set is
// deliberately OPEN — a floor may require an external supplier's mechanism
// ("dafny-proof"), declared in the invoker's intake config, so a closed
// enum here would block the intake contract. The typo risk this admits
// fails CLOSED: a misspelled mechanism is an unsatisfiable floor, and the
// miss row shows the best evidence beside the requirement.
var mechanismRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

var knownScopes = map[string]bool{
	schema.ScopeSinglePackage: true, schema.ScopeCrossPackage: true,
}

// Load reads and validates the repo's policy file. A missing file is
// (nil, nil) — no policy. Any other failure is an error: unreadable,
// unparseable, or invalid policies fail loudly before a single probe runs.
func Load(root string) (*Policy, error) {
	data, err := os.ReadFile(filepath.Join(root, File))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", File, err)
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %v", File, err)
	}
	if p.PolicyVersion != 1 {
		return nil, fmt.Errorf("%s: policy_version %d is not supported (want 1)", File, p.PolicyVersion)
	}
	if len(p.Rules) == 0 {
		return nil, fmt.Errorf("%s: no rules — delete the file to declare no policy", File)
	}
	for i, r := range p.Rules {
		if len(r.Paths) == 0 {
			return nil, fmt.Errorf("%s: rule %d has no paths", File, i)
		}
		if r.MinTier < 1 || r.MinTier > 4 {
			return nil, fmt.Errorf("%s: rule %d min_tier %d out of range (1–4)", File, i, r.MinTier)
		}
		if r.Mechanism != "" && !mechanismRe.MatchString(r.Mechanism) {
			return nil, fmt.Errorf("%s: rule %d mechanism %q is not a lowercase token", File, i, r.Mechanism)
		}
		if r.Scope != "" && !knownScopes[r.Scope] {
			return nil, fmt.Errorf("%s: rule %d names unknown scope %q", File, i, r.Scope)
		}
	}
	p.digest = fmt.Sprintf("%x", sha256.Sum256(data))
	return &p, nil
}

// Evaluate checks every changed file against every rule and returns the
// receipt's policy section. Each rule stands alone: a file matched by two
// rules must satisfy both floors.
func Evaluate(p *Policy, r schema.Receipt) *schema.PolicyResult {
	res := &schema.PolicyResult{Path: File, Digest: p.digest, Rules: len(p.Rules)}
	for _, rule := range p.Rules {
		for _, f := range r.Change.Files {
			if !matchesAny(rule.Paths, f) {
				continue
			}
			if isTestFile(f) {
				res.ExemptTestFiles++
				continue
			}
			if detail := floorMiss(rule, f, r.Results); detail != "" {
				res.Misses = append(res.Misses, schema.PolicyMiss{
					File: f, Rule: ruleLabel(rule), Detail: detail,
				})
			}
		}
	}
	return res
}

// floorMiss reports why file f fails rule's floor, or "" when satisfied:
// some verified claim must tie to f with evidence meeting the floor. The
// miss detail names the best tied evidence so the reader sees the gap, not
// just the verdict.
func floorMiss(rule Rule, f string, results []schema.ClaimResult) string {
	var best *schema.Evidence
	for i := range results {
		res := &results[i]
		if res.Status != schema.StatusVerified || !tiesTo(res.Claim, f) {
			continue
		}
		for j := range res.Evidence {
			e := &res.Evidence[j]
			if !e.CountsFor(res.Claim) {
				continue
			}
			if meetsFloor(rule, e) {
				return ""
			}
			if best == nil || e.Tier > best.Tier {
				best = e
			}
		}
	}
	floor := floorLabel(rule)
	if best == nil {
		return "no verified claim ties to this file; floor is " + floor
	}
	got := best.Tier.String() + " " + best.Mechanism
	if best.Scope != "" {
		got += " " + best.Scope
	}
	return "best tied evidence is " + got + "; floor is " + floor
}

// tiesTo reports whether a claim's evidence can speak for file f: the claim
// was sourced from f (LLM claims, spec-ids in code), or shipped code in f
// names the claim's id (a reference site).
func tiesTo(c schema.Claim, f string) bool {
	if c.Source.File == f {
		return true
	}
	for _, s := range c.RefSites {
		if s.File == f {
			return true
		}
	}
	return false
}

// meetsFloor checks one evidence row against a rule's requirements.
func meetsFloor(rule Rule, e *schema.Evidence) bool {
	if e.Tier < schema.Tier(rule.MinTier) {
		return false
	}
	if rule.Mechanism != "" && e.Mechanism != rule.Mechanism {
		return false
	}
	if rule.Scope != "" && e.Scope != rule.Scope {
		return false
	}
	return true
}

// isTestFile exempts files that are themselves probe sources — evidence,
// not evidence subjects. The exemption is counted and disclosed.
func isTestFile(f string) bool {
	return strings.HasSuffix(f, "_test.go")
}

// matchesAny reports whether f matches any of the rule's path patterns.
func matchesAny(patterns []string, f string) bool {
	for _, pat := range patterns {
		if matchesPattern(pat, f) {
			return true
		}
	}
	return false
}

// matchesPattern matches one pattern: exact path, "dir/..." subtree, or a
// path.Match glob (single-segment wildcards; * never crosses a slash).
func matchesPattern(pattern, f string) bool {
	if strings.HasSuffix(pattern, "/...") {
		return strings.HasPrefix(f, strings.TrimSuffix(pattern, "..."))
	}
	if pattern == f {
		return true
	}
	ok, err := path.Match(pattern, f)
	return err == nil && ok
}

// ruleLabel names a rule for a miss row.
func ruleLabel(r Rule) string {
	if r.Name != "" {
		return r.Name
	}
	return strings.Join(r.Paths, " ")
}

// floorLabel renders a rule's requirements the way a reader compares them.
func floorLabel(r Rule) string {
	s := "≥" + schema.Tier(r.MinTier).String()
	if r.Mechanism != "" {
		s += " " + r.Mechanism
	}
	if r.Scope != "" {
		s += " " + r.Scope
	}
	return s
}
