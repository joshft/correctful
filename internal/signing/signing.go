// Package signing authenticates receipts: an Ed25519 signature over the
// domain-separated canonical payload, minted by a process that runs no
// probes and reads no repository tree.
//
// The separation is the security design, not a convenience. The receipt
// producer builds and executes the change under review — its tests run as
// the same user, so any key that process can read, reviewed code can read.
// Signing therefore lives in its own subcommand fed an already-produced
// receipt, and the probe-running command has no signing flag to misuse. In
// CI that means three steps: produce (no key present), sign (key present,
// no reviewed code runs), verify (protected workflow, pinned public key).
//
// What a valid signature proves: the holder of the private key produced
// exactly this canonical content. What it does not prove: that the runner
// ran honestly, that the key was never stolen, or that this receipt is the
// freshest run for its subject — replay among same-subject runs is the
// verifier's freshness policy, stated in the docs, not solved here.
package signing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/joshft/correctful/internal/receipt"
	"github.com/joshft/correctful/internal/strictjson"
	"github.com/joshft/correctful/schema"
)

// domain separates receipt signatures from any other use of the same key,
// and versions the canonicalization: bytes signed under a future v2 can
// never verify as v1.
const domain = "correctful-receipt-v1\x00"

// MaxArtifactBytes bounds every receipt artifact this package will parse —
// unbounded input must never transit into memory unchecked.
const MaxArtifactBytes = 16 << 20

// Expect pins what the verifier requires beyond a valid signature. A
// signature alone authenticates SOME receipt by SOME run — subject matching
// is what ties it to the change the caller is gating, so HeadSHA is
// mandatory unless AnySubject explicitly waives it (authenticity-only
// checks of archived receipts).
type Expect struct {
	Audience    string
	HeadSHA     string
	BaseSHA     string // optional extra pin
	InputDigest string // optional extra pin
	AnySubject  bool
}

// Sign validates the receipt's internal consistency, signs its canonical
// payload, and returns the receipt with the signature block set. It refuses
// an already-signed receipt — re-signing must be an explicit decision made
// on the unsigned artifact, never a silent overwrite.
func Sign(r schema.Receipt, priv ed25519.PrivateKey, audience string) (schema.Receipt, error) {
	if r.Signature != nil {
		return r, fmt.Errorf("receipt is already signed (by key %s); sign the unsigned artifact", shortKey(r.Signature.PublicKey))
	}
	if err := checkAudience(audience); err != nil {
		return r, err
	}
	if err := receipt.ValidateConsistency(r); err != nil {
		return r, fmt.Errorf("refusing to sign an inconsistent receipt: %w", err)
	}
	payload, err := receipt.Canonical(r)
	if err != nil {
		return r, err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok || len(priv) != ed25519.PrivateKeySize {
		return r, fmt.Errorf("private key is not a usable ed25519 key")
	}
	sig := ed25519.Sign(priv, preimage(audience, payload))
	r.Signature = &schema.SignatureBlock{
		Alg:       "ed25519",
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Audience:  audience,
		Sig:       base64.StdEncoding.EncodeToString(sig),
	}
	return r, nil
}

// Verify authenticates a signed receipt artifact against a trusted public
// key and the caller's expectations, returning the parsed receipt on
// success. Every check fails loudly with its own reason:
//
//  1. strict parse — unknown fields, duplicate keys, trailing bytes, and
//     invalid UTF-8 are all differentials, not formatting;
//  2. canonical byte-identity — the artifact must BE its own canonical
//     re-encoding, so no two byte-forms of one receipt both verify;
//  3. signature — over the domain-separated payload with the block absent,
//     using the CALLER's key (the embedded key is an identity claim and
//     must match it; trusting the embedded key alone verifies nothing);
//  4. consistency — a signature authenticates bytes, not coherence;
//  5. subject — the receipt must be about the change the caller is gating.
func Verify(artifact []byte, trusted ed25519.PublicKey, exp Expect) (schema.Receipt, error) {
	var r schema.Receipt
	if len(artifact) > MaxArtifactBytes {
		return r, fmt.Errorf("artifact exceeds %d bytes", MaxArtifactBytes)
	}
	if err := strictjson.Decode(artifact, &r); err != nil {
		return r, fmt.Errorf("parsing receipt: %w", err)
	}
	canon, err := receipt.Canonical(r)
	if err != nil {
		return r, err
	}
	if !bytes.Equal(canon, artifact) {
		return r, fmt.Errorf("artifact is not in canonical form — the signature covers canonical receipt content, and a non-canonical byte-form is a parser differential, not formatting")
	}

	b := r.Signature
	if b == nil {
		return r, fmt.Errorf("receipt carries no signature")
	}
	if b.Alg != "ed25519" {
		return r, fmt.Errorf("unsupported signature alg %q (only ed25519)", b.Alg)
	}
	if err := checkAudience(b.Audience); err != nil {
		return r, err
	}
	pub, err := decodeB64(b.PublicKey, ed25519.PublicKeySize, "public key")
	if err != nil {
		return r, err
	}
	sig, err := decodeB64(b.Sig, ed25519.SignatureSize, "signature")
	if err != nil {
		return r, err
	}
	if len(trusted) != ed25519.PublicKeySize {
		return r, fmt.Errorf("trusted key is not a usable ed25519 public key")
	}
	if !bytes.Equal(pub, trusted) {
		return r, fmt.Errorf("receipt is signed by %s, not the trusted key %s", shortKey(b.PublicKey), shortKey(base64.StdEncoding.EncodeToString(trusted)))
	}
	if b.Audience != exp.Audience {
		return r, fmt.Errorf("audience mismatch: receipt is bound to %q, verifier expects %q", b.Audience, exp.Audience)
	}

	unsigned := r
	unsigned.Signature = nil
	payload, err := receipt.Canonical(unsigned)
	if err != nil {
		return r, err
	}
	if !ed25519.Verify(trusted, preimage(b.Audience, payload), sig) {
		return r, fmt.Errorf("signature is invalid for this content")
	}

	if err := receipt.ValidateConsistency(r); err != nil {
		return r, fmt.Errorf("authenticated receipt is internally inconsistent: %w", err)
	}

	if !exp.AnySubject {
		if exp.HeadSHA == "" {
			return r, fmt.Errorf("no expected head SHA: a signature authenticates SOME receipt — pass the change under review, or -any-subject to explicitly skip subject matching")
		}
		if r.Change.HeadSHA != exp.HeadSHA {
			return r, fmt.Errorf("subject mismatch: receipt is for head %s, expected %s", short(r.Change.HeadSHA), short(exp.HeadSHA))
		}
		if exp.BaseSHA != "" && r.Change.BaseSHA != exp.BaseSHA {
			return r, fmt.Errorf("subject mismatch: receipt is for base %s, expected %s", short(r.Change.BaseSHA), short(exp.BaseSHA))
		}
		if exp.InputDigest != "" && r.Change.InputDigest != exp.InputDigest {
			return r, fmt.Errorf("subject mismatch: receipt pins input digest %s, expected %s", short(r.Change.InputDigest), short(exp.InputDigest))
		}
	}
	return r, nil
}

func preimage(audience string, payload []byte) []byte {
	out := make([]byte, 0, len(domain)+len(audience)+1+len(payload))
	out = append(out, domain...)
	out = append(out, audience...)
	out = append(out, 0)
	out = append(out, payload...)
	return out
}

// checkAudience keeps the preimage unambiguous and the "control-free"
// documentation true: any control rune — ASCII C0, DEL, or the C1 range
// (0x80–0x9F), which the ASCII-only check used to admit — is refused, so a
// receipt cannot carry an audience with an invisible or line-breaking rune.
func checkAudience(a string) error {
	if len(a) > 200 {
		return fmt.Errorf("audience exceeds 200 bytes")
	}
	if !utf8.ValidString(a) {
		return fmt.Errorf("audience is not valid UTF-8")
	}
	for _, c := range a {
		if unicode.IsControl(c) {
			return fmt.Errorf("audience contains a control character")
		}
	}
	return nil
}

// decodeB64 accepts exactly one byte-form per value: strict standard
// base64, no whitespace, and a re-encode that reproduces the input — so a
// padding-bit or inserted-newline variant of the same raw bytes cannot
// yield a second verifying artifact.
func decodeB64(s string, wantLen int, what string) ([]byte, error) {
	if strings.ContainsAny(s, " \t\r\n") {
		return nil, fmt.Errorf("%s base64 contains whitespace", what)
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%s base64: %v", what, err)
	}
	if len(raw) != wantLen {
		return nil, fmt.Errorf("%s is %d bytes, want %d", what, len(raw), wantLen)
	}
	if base64.StdEncoding.EncodeToString(raw) != s {
		return nil, fmt.Errorf("%s base64 is not canonical", what)
	}
	return raw, nil
}

// shortKey abbreviates a key for an error message. The value can come from
// a hostile receipt (Sign's already-signed refusal, Verify's wrong-key
// error both quote the embedded public_key), so control runes are stripped
// before it reaches a terminal — an error string must not carry an escape
// sequence.
func shortKey(b64 string) string {
	b64 = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, b64)
	if len(b64) > 12 {
		return b64[:12] + "…"
	}
	return b64
}

func short(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
