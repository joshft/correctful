package receipt

import (
	"strings"
	"unicode"

	"github.com/joshft/correctful/schema"
)

// clean strips every control rune from a display string: ESC (terminal
// escape injection), CR/LF (breaking out of a markdown code span or table
// cell to inject a heading), and the DEL/C1 range. A receipt rendered to a
// terminal or a PR comment is UNTRUSTED — render does not verify, and even
// a verified receipt carries attacker-authored claim text — so no field may
// carry an active control sequence into the reader's terminal or a
// structural break into the Markdown. Legitimate fields are single-line
// printable text, so dropping controls is lossless for honest receipts.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// scrubForDisplay returns a copy of the receipt with every string field
// control-stripped. Both renderers call it first, so the control-injection
// defense lives in ONE place and cannot be bypassed by a print site that
// forgets to wrap a field. Digests and base64 are cleaned too even though
// they are already constrained shapes — a hostile receipt is not obligated
// to honor them, and cleaning costs nothing.
func scrubForDisplay(r schema.Receipt) schema.Receipt {
	r.Change = scrubChange(r.Change)
	r.Results = scrubResults(r.Results)
	r.Remainder = scrubResults(r.Remainder)
	r.Coverage = scrubCoverage(r.Coverage)
	if r.Policy != nil {
		p := *r.Policy
		p.Path = clean(p.Path)
		p.Digest = clean(p.Digest)
		misses := make([]schema.PolicyMiss, len(p.Misses))
		for i, m := range p.Misses {
			misses[i] = schema.PolicyMiss{File: clean(m.File), Rule: clean(m.Rule), Detail: clean(m.Detail)}
		}
		p.Misses = misses
		r.Policy = &p
	}
	if len(r.Intake) > 0 {
		intake := make([]schema.IntakeRecord, len(r.Intake))
		for i, rec := range r.Intake {
			rec.Supplier = clean(rec.Supplier)
			rec.SupplierVersion = clean(rec.SupplierVersion)
			rec.ConfigDigest = clean(rec.ConfigDigest)
			rec.Mechanism = clean(rec.Mechanism)
			rec.Reason = clean(rec.Reason)
			rec.DocDigest = clean(rec.DocDigest)
			rej := make([]schema.IntakeRejection, len(rec.Rejected))
			for j, x := range rec.Rejected {
				rej[j] = schema.IntakeRejection{
					ClaimID: clean(x.ClaimID), ProbeID: clean(x.ProbeID),
					Outcome: clean(x.Outcome), Reason: clean(x.Reason),
				}
			}
			rec.Rejected = rej
			intake[i] = rec
		}
		r.Intake = intake
	}
	if r.Signature != nil {
		b := *r.Signature
		b.Alg = clean(b.Alg)
		b.PublicKey = clean(b.PublicKey)
		b.Audience = clean(b.Audience)
		b.Sig = clean(b.Sig)
		r.Signature = &b
	}
	r.ToolVersion = clean(r.ToolVersion)
	r.SchemaVersion = clean(r.SchemaVersion)
	return r
}

func scrubChange(c schema.ChangeRef) schema.ChangeRef {
	c.Repo = clean(c.Repo)
	c.BaseRef = clean(c.BaseRef)
	c.HeadRef = clean(c.HeadRef)
	c.BaseSHA = clean(c.BaseSHA)
	c.HeadSHA = clean(c.HeadSHA)
	c.InputDigest = clean(c.InputDigest)
	c.Files = cleanSlice(c.Files)
	if len(c.Excluded) > 0 {
		ex := make([]schema.Exclusion, len(c.Excluded))
		for i, e := range c.Excluded {
			ex[i] = schema.Exclusion{Reason: clean(e.Reason), Count: e.Count, Dirs: cleanSlice(e.Dirs)}
		}
		c.Excluded = ex
	}
	return c
}

func scrubResults(rs []schema.ClaimResult) []schema.ClaimResult {
	if len(rs) == 0 {
		return rs
	}
	out := make([]schema.ClaimResult, len(rs))
	for i, res := range rs {
		res.Claim = scrubClaim(res.Claim)
		res.Evidence = scrubEvidence(res.Evidence)
		out[i] = res
	}
	return out
}

func scrubClaim(c schema.Claim) schema.Claim {
	c.ID = clean(c.ID)
	c.Text = clean(c.Text)
	c.Source = scrubSource(c.Source)
	c.ProbeIDs = cleanSlice(c.ProbeIDs)
	if c.Anchor != nil {
		a := *c.Anchor
		a.Title = clean(a.Title)
		a.Sites = scrubSources(a.Sites)
		c.Anchor = &a
	}
	c.RefSites = scrubSources(c.RefSites)
	return c
}

func scrubSource(s schema.Source) schema.Source {
	s.File = clean(s.File)
	s.Ref = clean(s.Ref)
	return s
}

func scrubSources(ss []schema.Source) []schema.Source {
	if len(ss) == 0 {
		return ss
	}
	out := make([]schema.Source, len(ss))
	for i, s := range ss {
		out[i] = scrubSource(s)
	}
	return out
}

func scrubEvidence(es []schema.Evidence) []schema.Evidence {
	if len(es) == 0 {
		return es
	}
	out := make([]schema.Evidence, len(es))
	for i, e := range es {
		e.ClaimID = clean(e.ClaimID)
		e.ProbeID = clean(e.ProbeID)
		e.Detail = clean(e.Detail)
		e.Duration = clean(e.Duration)
		e.Binding = clean(e.Binding)
		e.Mechanism = clean(e.Mechanism)
		e.Scope = clean(e.Scope)
		e.Environment = clean(e.Environment)
		e.Supplier = clean(e.Supplier)
		out[i] = e
	}
	return out
}

func scrubCoverage(c schema.Coverage) schema.Coverage {
	if len(c.Files) == 0 {
		return c
	}
	files := make([]schema.FileCoverage, len(c.Files))
	for i, fc := range c.Files {
		fc.File = clean(fc.File)
		fc.ReadBy = cleanSlice(fc.ReadBy)
		fc.SkipReason = clean(fc.SkipReason)
		files[i] = fc
	}
	c.Files = files
	return c
}

func cleanSlice(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = clean(s)
	}
	return out
}
