// Package intake admits evidence from EXTERNAL probe suppliers — fuzzers,
// proof workers, observation planes — through a schema-shaped contract, so
// correctful validates, binds, combines, and applies policy without becoming
// every probe runner.
//
// The trust model is explicit, because correctful cannot re-execute an
// external probe. Authority lives in an invoker-owned PROFILE, never in the
// document: the profile fixes the supplier's name, mechanism, and maximum
// tier (the external analogue of Runner.MaxTier), and a row reports an
// OUTCOME only — it cannot select its own tier, mechanism, or supplier.
// Admission requires a subject match (head SHA and input digest both), and
// every admitted row is marked Binding "supplier-attested": the residual
// trust — the supplier could still lie, omit work, or bind the wrong
// property — is the supplier's word, and the receipt says so on the row.
// A signature channel is the planned stronger leg; until then, admission
// authenticates possession of the invoker's config, not origin.
//
// Fail-closed and fail-loud rules:
//   - A malformed config or document is a loud error before any probe runs.
//   - Config and documents must be regular files OUTSIDE the repository
//     tree (symlinks rejected): evidence readable from the reviewed change's
//     own tree would let the change mint evidence for its own claims.
//   - A subject mismatch rejects the WHOLE document (stale — about other
//     content), recorded and disclosed, never silently dropped.
//   - A required supplier with no admitted document blocks the gate.
//   - Outcomes are an enum, not booleans: only "counterexample" refutes.
//     "inconclusive", "not_run", and "error" are Ran=false — an unproved
//     proof or a fuzzer timeout must never read as a refutation (the same
//     lesson the go-test runners learned from t.Skip's exit 0).
package intake

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/joshft/correctful/schema"
)

// Bounds — strict limits so a document cannot balloon a receipt.
const (
	maxSuppliers   = 16
	maxConfigBytes = 64 << 10
	maxDocBytes    = 4 << 20
	maxRows        = 500
	maxDetailLen   = 300
	maxProbeIDLen  = 200
)

// Config is the invoker-owned intake configuration: the authority grants.
type Config struct {
	IntakeVersion int       `json:"intake_version"`
	Suppliers     []Profile `json:"suppliers"`

	digest string // SHA-256 over the config's exact bytes — the authority pin
}

// Profile is one supplier's authority grant.
type Profile struct {
	// Name identifies the supplier; token shape, and the value every
	// admitted row's Evidence.Supplier carries.
	Name string `json:"name"`
	// Version identifies the supplier build the invoker vouches for —
	// echoed into the receipt's intake record so two receipts are
	// comparable across supplier upgrades. Optional.
	Version string `json:"version,omitempty"`
	// Mechanism is the evidence class the invoker vouches this supplier
	// produces (e.g. "dafny-proof"). Policy floors reference it. Must not
	// collide with a built-in runner mechanism.
	Mechanism string `json:"mechanism"`
	// MaxTier is the tier a verified outcome from this supplier confers —
	// the authority clamp. Rows carry no tier at all.
	MaxTier int `json:"max_tier"`
	// Document is the path to this supplier's intake document. Must live
	// outside the repository tree.
	Document string `json:"document"`
	// Required blocks the gate when no document is admitted.
	Required bool `json:"required,omitempty"`
}

// Document is one supplier run's report.
type document struct {
	IntakeVersion int     `json:"intake_version"`
	Supplier      string  `json:"supplier"`
	Subject       subject `json:"subject"`
	Results       []row   `json:"results"`
}

// subject pins WHAT the supplier's probes ran against. Both fields must
// match the receipt's own change identity. Known boundary, disclosed in
// DESIGN.md: this names the committed tree plus the changed-file overlay,
// not the full dependency closure.
type subject struct {
	HeadSHA     string `json:"head_sha"`
	InputDigest string `json:"input_digest"`
}

// row is one probe outcome. No tier, no mechanism, no supplier — authority
// is the profile's, not the row's.
type row struct {
	ClaimID  string `json:"claim_id"`
	ProbeID  string `json:"probe_id"`
	Outcome  string `json:"outcome"`
	Detail   string `json:"detail"`
	Duration string `json:"duration"`
}

// Outcome vocabulary. Only OutcomeCounterexample refutes.
const (
	OutcomeVerified       = "verified"
	OutcomeCounterexample = "counterexample"
	OutcomeInconclusive   = "inconclusive"
	OutcomeNotRun         = "not_run"
	OutcomeError          = "error"
)

var validOutcomes = map[string]bool{
	OutcomeVerified: true, OutcomeCounterexample: true,
	OutcomeInconclusive: true, OutcomeNotRun: true, OutcomeError: true,
}

// tokenRe is the shape for supplier names and mechanisms.
var tokenRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// hexDigestRe is a canonical SHA-256 hex digest.
var hexDigestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// builtinMechanisms a profile must not claim — an external supplier cannot
// masquerade as an in-tree runner.
var builtinMechanisms = map[string]bool{
	schema.MechanismGoTest: true, schema.MechanismGoTestPair: true,
	schema.MechanismDotnetTest: true, schema.MechanismAlloyCheck: true,
}

// LoadConfig reads and validates the intake config. Loud on every failure —
// a broken authority grant must never fail open. repoRoot guards the
// out-of-tree rule for the config itself and every document path.
func LoadConfig(path, repoRoot string) (*Config, error) {
	data, err := readOutsideTree(path, repoRoot, maxConfigBytes)
	if err != nil {
		return nil, fmt.Errorf("intake config: %w", err)
	}
	var c Config
	if err := strictDecode(data, &c); err != nil {
		return nil, fmt.Errorf("intake config %s: %w", path, err)
	}
	if c.IntakeVersion != 1 {
		return nil, fmt.Errorf("intake config: intake_version %d is not supported (want 1)", c.IntakeVersion)
	}
	if len(c.Suppliers) == 0 || len(c.Suppliers) > maxSuppliers {
		return nil, fmt.Errorf("intake config: %d suppliers (want 1–%d)", len(c.Suppliers), maxSuppliers)
	}
	seen := map[string]bool{}
	for i, p := range c.Suppliers {
		switch {
		case !tokenRe.MatchString(p.Name):
			return nil, fmt.Errorf("intake config: supplier %d name %q is not a lowercase token", i, p.Name)
		case seen[p.Name]:
			return nil, fmt.Errorf("intake config: duplicate supplier %q", p.Name)
		case !tokenRe.MatchString(p.Mechanism):
			return nil, fmt.Errorf("intake config: supplier %q mechanism %q is not a lowercase token", p.Name, p.Mechanism)
		case builtinMechanisms[p.Mechanism]:
			return nil, fmt.Errorf("intake config: supplier %q claims built-in mechanism %q", p.Name, p.Mechanism)
		case p.MaxTier < 1 || p.MaxTier > 4:
			return nil, fmt.Errorf("intake config: supplier %q max_tier %d out of range (1–4)", p.Name, p.MaxTier)
		case p.Document == "":
			return nil, fmt.Errorf("intake config: supplier %q has no document path", p.Name)
		case len(p.Version) > 64:
			return nil, fmt.Errorf("intake config: supplier %q version exceeds 64 bytes", p.Name)
		}
		seen[p.Name] = true
	}
	c.digest = fmt.Sprintf("%x", sha256.Sum256(data))
	return &c, nil
}

// Subject is the receipt-side change identity documents must match.
type Subject struct {
	HeadSHA     string
	InputDigest string
}

// Run admits each profile's document and converts accepted rows into
// evidence, keyed by claim id. Every profile yields an IntakeRecord —
// including profiles whose document was missing or rejected.
func Run(c *Config, repoRoot string, subj Subject, claims []schema.Claim) (map[string][]schema.Evidence, []schema.IntakeRecord, error) {
	claimByID := make(map[string]*schema.Claim, len(claims))
	for i := range claims {
		claimByID[claims[i].ID] = &claims[i]
	}
	extra := map[string][]schema.Evidence{}
	var records []schema.IntakeRecord
	// Duplicates are per SUPPLIER, keyed by outcome too: two suppliers may
	// legitimately probe the same target (their evidence is distinct — the
	// namespaced probe ids differ), so a global key would let one
	// supplier's pass suppress another's counterexample, violating
	// refutation dominance (found adversarially, live). Within a supplier,
	// a contradictory duplicate is a supplier bug and fails LOUDLY —
	// silently keeping either verdict could launder the other away.
	seenOutcome := map[string]string{} // supplier\x00claim\x00probe -> outcome

	for _, p := range c.Suppliers {
		rec := schema.IntakeRecord{Supplier: p.Name, SupplierVersion: scrub(p.Version),
			Mechanism: p.Mechanism, MaxTier: schema.Tier(p.MaxTier),
			Required: p.Required, ConfigDigest: c.digest}
		doc, digest, reason, err := admit(p, repoRoot, subj)
		if err != nil {
			return nil, nil, err
		}
		if doc == nil {
			rec.Reason = reason
			records = append(records, rec)
			continue
		}
		rec.Admitted, rec.DocDigest = true, digest
		for _, r := range doc.Results {
			key := p.Name + "\x00" + r.ClaimID + "\x00" + r.ProbeID
			if prev, dup := seenOutcome[key]; dup && prev != r.Outcome {
				return nil, nil, fmt.Errorf("intake document for %q: contradictory verdicts for %s / %s (%s vs %s)",
					p.Name, r.ClaimID, r.ProbeID, prev, r.Outcome)
			}
			if reason := rejectRow(r, claimByID, seenOutcome, key); reason != "" {
				rec.Rejected = append(rec.Rejected, schema.IntakeRejection{
					ClaimID: scrub(clip(r.ClaimID, maxProbeIDLen)), ProbeID: scrub(clip(r.ProbeID, maxProbeIDLen)),
					Outcome: scrub(clip(r.Outcome, 32)), Reason: reason,
				})
				continue
			}
			seenOutcome[key] = r.Outcome
			ev := evidenceFrom(p, r)
			extra[r.ClaimID] = append(extra[r.ClaimID], ev)
			rec.Accepted++
		}
		records = append(records, rec)
	}
	return extra, records, nil
}

// admit loads one supplier's document and checks the admission gates. A
// missing or mismatched document is (nil, reason) — recorded, not an error;
// a malformed one IS an error, same as a malformed config.
func admit(p Profile, repoRoot string, subj Subject) (*document, string, string, error) {
	data, err := readOutsideTree(p.Document, repoRoot, maxDocBytes)
	if os.IsNotExist(err) {
		return nil, "", "no document at the configured path", nil
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("intake document for %q: %w", p.Name, err)
	}
	var doc document
	if err := strictDecode(data, &doc); err != nil {
		return nil, "", "", fmt.Errorf("intake document for %q: %w", p.Name, err)
	}
	switch {
	case doc.IntakeVersion != 1:
		return nil, "", "", fmt.Errorf("intake document for %q: intake_version %d is not supported (want 1)", p.Name, doc.IntakeVersion)
	case len(doc.Results) > maxRows:
		return nil, "", "", fmt.Errorf("intake document for %q: %d rows exceeds the %d-row bound", p.Name, len(doc.Results), maxRows)
	case doc.Supplier != p.Name:
		return nil, "", fmt.Sprintf("document names supplier %q, profile is %q", clip(doc.Supplier, 64), p.Name), nil
	case !hexDigestRe.MatchString(doc.Subject.InputDigest):
		return nil, "", "subject input_digest is not a canonical sha256 hex digest", nil
	case doc.Subject.HeadSHA != subj.HeadSHA || doc.Subject.InputDigest != subj.InputDigest:
		return nil, "", "subject mismatch — the evidence is about different content", nil
	}
	return &doc, fmt.Sprintf("%x", sha256.Sum256(data)), "", nil
}

// rejectRow returns the reason a row does not become evidence, or "".
func rejectRow(r row, claims map[string]*schema.Claim, seen map[string]string, key string) string {
	switch {
	case !validOutcomes[r.Outcome]:
		return "unknown outcome (want verified, counterexample, inconclusive, not_run, or error)"
	case r.ProbeID == "" || len(r.ProbeID) > maxProbeIDLen:
		return "probe_id empty or too long"
	case r.ClaimID == "":
		return "claim_id empty"
	}
	if _, dup := seen[key]; dup {
		return "duplicate (claim_id, probe_id) for this supplier"
	}
	c, ok := claims[r.ClaimID]
	if !ok {
		return "no such claim in this change"
	}
	if c.Anchor != nil && c.Anchor.Status == schema.AnchorAmbiguous {
		return "claim id is ambiguously anchored — the evidence cannot say which definition it verified"
	}
	return ""
}

// evidenceFrom converts an accepted row. Tier, mechanism, and supplier come
// from the PROFILE; the probe id is namespaced by construction (never
// trusted to avoid built-in prefixes — it cannot collide with them).
func evidenceFrom(p Profile, r row) schema.Evidence {
	ev := schema.Evidence{
		ClaimID:   r.ClaimID,
		ProbeID:   "ext:" + p.Name + "/" + scrub(clip(r.ProbeID, maxProbeIDLen)),
		Tier:      schema.Tier(p.MaxTier),
		Mechanism: p.Mechanism,
		Supplier:  p.Name,
		Binding:   schema.BindingSupplierAttested,
		Detail:    scrub(clip(r.Detail, maxDetailLen)),
		Duration:  scrub(clip(r.Duration, 32)),
	}
	switch r.Outcome {
	case OutcomeVerified:
		ev.Ran, ev.Passed = true, true
		if ev.Detail == "" {
			ev.Detail = "supplier reported verified"
		}
	case OutcomeCounterexample:
		ev.Ran, ev.Passed = true, false
		if ev.Detail == "" {
			ev.Detail = "supplier reported a counterexample"
		}
	default: // inconclusive, not_run, error — never a verdict
		ev.Ran, ev.Passed = false, false
		ev.Detail = strings.TrimSpace(r.Outcome + " — " + ev.Detail)
	}
	return ev
}

// readOutsideTree reads one intake file with the boundary checks the
// contract depends on, hardened adversarially:
//
//   - CANONICAL containment, not lexical: every path component of both the
//     file's directory and the repo root is symlink-resolved before the
//     prefix comparison — a symlinked parent directory was demonstrated to
//     smuggle an in-tree file past a lexical check.
//   - The final component must not be a symlink, enforced at open time
//     with O_NOFOLLOW — an Lstat-then-open pair leaves a swap window.
//   - The size bound applies through a limited reader on the single opened
//     fd, and the fd's own Stat (not the path) decides regularity.
//
// A missing file returns the raw not-exist error so callers can treat
// absence as recordable rather than fatal.
func readOutsideTree(path, repoRoot string, maxBytes int64) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	canonDir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, err // includes not-exist for the parent
	}
	canon := filepath.Join(canonDir, filepath.Base(abs))
	canonRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return nil, err
	}
	if canon == canonRoot || strings.HasPrefix(canon, canonRoot+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s resolves inside the repository tree — the reviewed change must not supply its own evidence", path)
	}
	f, err := os.OpenFile(canon, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%s is a symlink — intake paths must be regular files", path)
		}
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds the %d-byte bound", path, maxBytes)
	}
	return data, nil
}

// strictDecode parses JSON with unknown fields rejected, trailing content
// refused, and DUPLICATE KEYS refused. The stdlib decoder silently keeps a
// duplicate's last value — demonstrated to smuggle a second "outcome":
// "verified" behind a "counterexample" — and last-wins parsing would also
// make any future signature ambiguous across JSON parsers.
func strictDecode(data []byte, v any) error {
	if err := rejectDupKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing content after the JSON document")
	}
	return nil
}

// rejectDupKeys walks the token stream and fails on a repeated object key
// at any depth.
func rejectDupKeys(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	return rejectDupKeysIn(dec, t)
}

func rejectDupKeysIn(dec *json.Decoder, t json.Token) error {
	d, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			k, _ := kt.(string)
			if seen[k] {
				return fmt.Errorf("duplicate key %q", k)
			}
			seen[k] = true
			vt, err := dec.Token()
			if err != nil {
				return err
			}
			if err := rejectDupKeysIn(dec, vt); err != nil {
				return err
			}
		}
		_, err := dec.Token() // consume '}'
		return err
	case '[':
		for dec.More() {
			vt, err := dec.Token()
			if err != nil {
				return err
			}
			if err := rejectDupKeysIn(dec, vt); err != nil {
				return err
			}
		}
		_, err := dec.Token() // consume ']'
		return err
	}
	return nil
}

// scrub strips control characters — C0 (except newline and tab), DEL, and
// the C1 range — from an external string: supplied text reaches terminals
// and PR comments, and must not carry escapes. Applied to EVERY stored
// external field, rejected rows included (a rejection renders too). Path
// scrubbing happens later at the receipt's sanitization chokepoint.
func scrub(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			return -1
		}
		return r
	}, s)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
