package signing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/internal/receipt"
	"github.com/joshft/correctful/schema"
)

// fixtureReceipt assembles a small internally consistent receipt: one
// verified claim, one refuted claim. ToolVersion is pinned so the fixture
// is byte-deterministic across build environments (the golden tests
// depend on that).
func fixtureReceipt(t *testing.T) schema.Receipt {
	t.Helper()
	change := gitdiff.Change{
		Repo:        "repo",
		BaseRef:     "main",
		HeadRef:     "feat",
		BaseSHA:     strings.Repeat("b", 40),
		HeadSHA:     strings.Repeat("h", 40),
		Files:       []string{"a.go", "a_test.go"},
		InputDigest: strings.Repeat("d", 64),
	}
	claims := []schema.Claim{
		{ID: "TestA", Text: "TestA passes", ProbeIDs: []string{"go-test/TestA"}},
		{ID: "TestB", Text: "TestB passes", ProbeIDs: []string{"go-test/TestB"}},
	}
	evidence := [][]schema.Evidence{
		{{ClaimID: "TestA", ProbeID: "go-test/TestA", Tier: schema.T1Assertion, Ran: true, Passed: true}},
		{{ClaimID: "TestB", ProbeID: "go-test/TestB", Tier: schema.T1Assertion, Ran: true, Passed: false}},
	}
	cov := schema.Coverage{
		Files: []schema.FileCoverage{
			{File: "a.go", ReadBy: []string{"gotest"}, Claims: 2},
			{File: "a_test.go", ReadBy: []string{"gotest"}},
		},
		Claimed: 1,
		Scanned: 1,
	}
	r := receipt.Assemble(change, claims, evidence, cov)
	r.ToolVersion = "test"
	return r
}

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv
}

func signedArtifact(t *testing.T, audience string) ([]byte, ed25519.PublicKey) {
	t.Helper()
	pub, priv := testKey(t)
	signed, err := Sign(fixtureReceipt(t), priv, audience)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	artifact, err := receipt.Canonical(signed)
	if err != nil {
		t.Fatal(err)
	}
	return artifact, pub
}

func expectFor(r schema.Receipt, audience string) Expect {
	return Expect{Audience: audience, HeadSHA: r.Change.HeadSHA}
}

// TestSignVerifyRoundTrip: the whole path — sign a consistent receipt,
// verify the canonical artifact against the trusted key and the expected
// subject. Ed25519 is deterministic, so signing twice must agree — a
// divergence would mean nondeterministic canonicalization.
func TestSignVerifyRoundTrip(t *testing.T) {
	artifact, pub := signedArtifact(t, "github.com/x/y")
	r := fixtureReceipt(t)
	got, err := Verify(artifact, pub, expectFor(r, "github.com/x/y"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Signature == nil || got.Signature.Alg != "ed25519" {
		t.Fatalf("verified receipt lost its signature block: %+v", got.Signature)
	}
	if !got.GateBlocked() {
		t.Errorf("fixture has a refuted claim; gate must report blocked")
	}

	again, _ := signedArtifact(t, "github.com/x/y")
	if !bytes.Equal(artifact, again) {
		t.Errorf("signing the same receipt twice produced different artifacts")
	}
}

// TestVerifyWrongKeyFails: a valid signature by SOME key is worthless —
// only the caller's pinned key authenticates.
func TestVerifyWrongKeyFails(t *testing.T) {
	artifact, _ := signedArtifact(t, "")
	otherPub, _, _ := ed25519.GenerateKey(nil)
	_, err := Verify(artifact, otherPub, expectFor(fixtureReceipt(t), ""))
	if err == nil || !strings.Contains(err.Error(), "not the trusted key") {
		t.Fatalf("wrong key accepted or wrong error: %v", err)
	}
}

// TestVerifyAudienceBinding: the audience is part of the signed preimage
// AND compared explicitly, so a receipt signed for one repository fails
// verification configured for another — with a named reason, not a bare
// "signature invalid".
func TestVerifyAudienceBinding(t *testing.T) {
	artifact, pub := signedArtifact(t, "github.com/x/y")
	_, err := Verify(artifact, pub, expectFor(fixtureReceipt(t), "github.com/x/OTHER"))
	if err == nil || !strings.Contains(err.Error(), "audience mismatch") {
		t.Fatalf("audience mismatch accepted or wrong error: %v", err)
	}

	// A forged block audience cannot help: rewriting it breaks the
	// signature because the audience is inside the preimage.
	var r schema.Receipt
	if err := json.Unmarshal(artifact, &r); err != nil {
		t.Fatal(err)
	}
	r.Signature.Audience = "github.com/x/OTHER"
	forged, err := receipt.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(forged, pub, expectFor(r, "github.com/x/OTHER"))
	if err == nil || !strings.Contains(err.Error(), "signature is invalid") {
		t.Fatalf("audience rewrite survived: %v", err)
	}
}

// TestVerifySubjectMatching: a signature authenticates SOME receipt; the
// subject match ties it to THE change. Head is mandatory unless the caller
// explicitly opts out.
func TestVerifySubjectMatching(t *testing.T) {
	artifact, pub := signedArtifact(t, "")

	if _, err := Verify(artifact, pub, Expect{HeadSHA: strings.Repeat("e", 40)}); err == nil || !strings.Contains(err.Error(), "subject mismatch") {
		t.Fatalf("wrong head accepted: %v", err)
	}
	if _, err := Verify(artifact, pub, Expect{}); err == nil || !strings.Contains(err.Error(), "-any-subject") {
		t.Fatalf("missing head must demand an explicit choice: %v", err)
	}
	if _, err := Verify(artifact, pub, Expect{AnySubject: true}); err != nil {
		t.Fatalf("explicit any-subject failed: %v", err)
	}
	r := fixtureReceipt(t)
	if _, err := Verify(artifact, pub, Expect{HeadSHA: r.Change.HeadSHA, InputDigest: strings.Repeat("f", 64)}); err == nil || !strings.Contains(err.Error(), "input digest") {
		t.Fatalf("wrong input digest accepted: %v", err)
	}
}

// TestVerifyRejectsNonCanonicalArtifact: exactly one byte-form of a
// receipt verifies. A compact re-encoding of the SAME content carries the
// same valid signature bytes — and must still fail, or two parsers could
// read one signed receipt two ways.
func TestVerifyRejectsNonCanonicalArtifact(t *testing.T) {
	artifact, pub := signedArtifact(t, "")
	exp := expectFor(fixtureReceipt(t), "")

	if _, err := Verify(append(artifact, '\n'), pub, exp); err == nil {
		t.Fatalf("trailing newline accepted")
	}

	var v any
	if err := json.Unmarshal(artifact, &v); err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(compact, pub, exp); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("compact re-encoding accepted: %v", err)
	}
}

// TestVerifyRejectsEveryByteMutation: flip one bit in every byte of the
// artifact; every mutation must fail SOMEWHERE — parse, canonical
// identity, or signature. No mutation may verify.
func TestVerifyRejectsEveryByteMutation(t *testing.T) {
	artifact, pub := signedArtifact(t, "github.com/x/y")
	exp := expectFor(fixtureReceipt(t), "github.com/x/y")
	for i := range artifact {
		mut := append([]byte(nil), artifact...)
		mut[i] ^= 0x01
		if _, err := Verify(mut, pub, exp); err == nil {
			t.Fatalf("bit flip at byte %d (%q) verified", i, artifact[i])
		}
	}
}

// TestSignRefusesInconsistentReceipt and TestVerifyRejectsSignedInconsistency:
// the codex-review attack — a wrapper zeroes Summary.Refuted around one
// refuted result, so GateBlocked (which reads the summary) passes. Our
// signer refuses to mint it, and even a signature minted by a BYPASSING
// signer fails verification, because consistency is validated on both
// sides of the trust boundary.
func TestSignRefusesInconsistentReceipt(t *testing.T) {
	_, priv := testKey(t)
	r := fixtureReceipt(t)
	r.Summary.Refuted = 0
	r.Summary.Verified = 2
	if _, err := Sign(r, priv, ""); err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("inconsistent receipt signed: %v", err)
	}
}

func TestVerifyRejectsSignedInconsistency(t *testing.T) {
	pub, priv := testKey(t)
	r := fixtureReceipt(t)
	r.Summary.Refuted = 0
	r.Summary.Verified = 2

	// A bypassing signer: raw ed25519 over the canonical payload, no
	// consistency check.
	payload, err := receipt.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, preimage("", payload))
	r.Signature = &schema.SignatureBlock{
		Alg:       "ed25519",
		PublicKey: b64(pub),
		Sig:       b64(sig),
	}
	artifact, err := receipt.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(artifact, pub, expectFor(r, ""))
	if err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("signed-but-inconsistent receipt verified: %v", err)
	}
}

// TestSignRefusesAlreadySigned: re-signing is an explicit decision made on
// the unsigned artifact, never a silent overwrite.
func TestSignRefusesAlreadySigned(t *testing.T) {
	_, priv := testKey(t)
	signed, err := Sign(fixtureReceipt(t), priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(signed, priv, ""); err == nil || !strings.Contains(err.Error(), "already signed") {
		t.Fatalf("double-sign allowed: %v", err)
	}
}

// TestSignRejectsControlAudience: a NUL or control byte in the audience
// could shift the preimage boundary between audience and payload.
func TestSignRejectsControlAudience(t *testing.T) {
	_, priv := testKey(t)
	for _, aud := range []string{"a\x00b", "a\nb", strings.Repeat("x", 201)} {
		if _, err := Sign(fixtureReceipt(t), priv, aud); err == nil {
			t.Fatalf("audience %q accepted", aud)
		}
	}
}

// TestVerifyRejectsMalleableBase64: the signature bytes admit exactly one
// base64 spelling. A whitespace-bearing or padding-bit variant decodes to
// the same raw bytes under a lenient decoder — a second verifying
// byte-form of the same receipt, which is exactly the differential the
// canonical contract exists to kill. (The canonical-identity check already
// rejects these; this pins the independent second leg in decodeB64.)
func TestVerifyRejectsMalleableBase64(t *testing.T) {
	if _, err := decodeB64("QUJ\nDRA==", 4, "test"); err == nil {
		t.Fatalf("whitespace base64 accepted")
	}
	// "QUJDRB==" decodes to the same 4 bytes as "QUJDRA==" under a lenient
	// decoder (nonzero trailing bits); strict must refuse.
	if _, err := decodeB64("QUJDRB==", 4, "test"); err == nil {
		t.Fatalf("noncanonical trailing bits accepted")
	}
}

// TestGoldenSignature freezes the whole pipeline — canonicalization, the
// domain separator, the preimage layout, and raw (non-prehashed) Ed25519 —
// as one pinned signature over the deterministic fixture. If this test
// breaks and you did not deliberately change the schema or the preimage,
// you have changed what old receipts verify as — stop.
func TestGoldenSignature(t *testing.T) {
	_, priv := testKey(t)
	signed, err := Sign(fixtureReceipt(t), priv, "github.com/x/y")
	if err != nil {
		t.Fatal(err)
	}
	const want = "0+lGRwafD0o8wR+4YUo4BU1Dg1McLAeyTum2h62ie1dKBYorzcNj244JvyGv/3zJzG+AnuBTjrQlxgTzW9hiAQ=="
	if got := signed.Signature.Sig; got != want {
		t.Fatalf("golden signature drifted:\n got %s\nwant %s", got, want)
	}
}

// TestRFC8032Vector pins the stdlib against RFC 8032 §7.1 TEST 3 — a
// sanity check that ed25519.Sign is the pure (non-prehashed) variant our
// preimage design assumes.
func TestRFC8032Vector(t *testing.T) {
	seed, _ := hex.DecodeString("c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7")
	msg, _ := hex.DecodeString("af82")
	wantSig, _ := hex.DecodeString("6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a")
	priv := ed25519.NewKeyFromSeed(seed)
	if got := ed25519.Sign(priv, msg); !bytes.Equal(got, wantSig) {
		t.Fatalf("stdlib ed25519 does not match RFC 8032 TEST 3")
	}
}

func b64(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}
