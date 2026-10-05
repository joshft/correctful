package signing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/receipt"
	"github.com/joshft/correctful/schema"
)

func currentReceipt(t *testing.T) schema.Receipt {
	r := fixtureReceipt(t)
	r.SchemaVersion = schema.SchemaVersion
	r.Gate = r.GateVerdict()
	return r
}

func TestSignedProducerAndVerdictRoundTrip(t *testing.T) {
	pub, priv := testKey(t)
	for _, role := range []string{"advisory", "gate", "completion"} {
		r := currentReceipt(t)
		signed, err := SignWithProducer(r, priv, "example", schema.ReceiptProducer{Runner: "independent-runner", Role: role})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := receipt.Canonical(signed)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Verify(raw, pub, Expect{Audience: "example", HeadSHA: r.Change.HeadSHA, Runner: "independent-runner", Role: role})
		if err != nil {
			t.Fatal(err)
		}
		if got.Gate != "refuted" || got.Producer == nil || got.Producer.Role != role {
			t.Fatalf("lost signed fields: %+v", got)
		}
		for _, exp := range []Expect{
			{Audience: "example", HeadSHA: r.Change.HeadSHA, Runner: "other-runner"},
			{Audience: "example", HeadSHA: r.Change.HeadSHA, Role: "other-role"},
		} {
			if _, err := Verify(raw, pub, exp); err == nil {
				t.Fatal("mismatched producer pin accepted")
			}
		}
	}
}

func TestTamperingEachSignedProducerFieldFails(t *testing.T) {
	pub, priv := testKey(t)
	for _, field := range []string{"runner", "role", "gate"} {
		r, err := SignWithProducer(currentReceipt(t), priv, "", schema.ReceiptProducer{Runner: "runner", Role: "gate"})
		if err != nil {
			t.Fatal(err)
		}
		switch field {
		case "runner":
			r.Producer.Runner = "other"
		case "role":
			r.Producer.Role = "completion"
		case "gate":
			r.Gate = "pass"
		}
		raw, err := receipt.Canonical(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(raw, pub, expectFor(r, "")); err == nil || !strings.Contains(err.Error(), "signature is invalid") {
			t.Fatalf("%s: %v", field, err)
		}
	}
}

func TestProducerControlledAuthorityIsRejected(t *testing.T) {
	_, priv := testKey(t)
	for _, p := range []schema.ReceiptProducer{{Runner: "model", Role: "gate"}, {Runner: "runner", Role: "gate"}} {
		r := currentReceipt(t)
		r.Producer = &p
		if _, err := SignWithProducer(r, priv, "", schema.ReceiptProducer{Runner: "runner", Role: "gate"}); err == nil {
			t.Fatal("producer-selected authority accepted")
		}
		if _, err := Sign(r, priv, ""); err == nil {
			t.Fatal("legacy signing entry bypassed configuration")
		}
	}
	for _, p := range []schema.ReceiptProducer{{}, {Runner: "runner", Role: "admin"}, {Runner: "runner\nforged", Role: "gate"}} {
		if _, err := SignWithProducer(currentReceipt(t), priv, "", p); err == nil {
			t.Fatal("invalid invoker configuration accepted")
		}
	}
}

func TestLegacyReceiptVerifiesWithoutNewClaims(t *testing.T) {
	raw, pub := signedArtifact(t, "")
	got, err := Verify(raw, pub, expectFor(fixtureReceipt(t), ""))
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != "0.0.14" || got.Producer != nil || got.Gate != "" {
		t.Fatal("legacy receipt acquired new claims")
	}
	if bytes.Contains(raw, []byte(`"producer"`)) || bytes.Contains(raw, []byte(`"gate"`)) {
		t.Fatal("legacy wire format changed")
	}
	if _, err := Verify(raw, pub, Expect{HeadSHA: got.Change.HeadSHA, Role: "gate"}); err == nil {
		t.Fatal("legacy receipt satisfied a new role pin")
	}
	_, priv := testKey(t)
	got.Signature = nil
	got.Gate = "pass"
	if _, err := Sign(got, priv, ""); err == nil {
		t.Fatal("legacy receipt accepted a new field")
	}
}

func TestSignedInconsistentVerdictFailsVerification(t *testing.T) {
	pub, priv := testKey(t)
	r := currentReceipt(t)
	r.Gate = "pass"
	if _, err := SignWithProducer(r, priv, "", schema.ReceiptProducer{Runner: "runner", Role: "gate"}); err == nil {
		t.Fatal("signer accepted false gate")
	}
	r.Producer = &schema.ReceiptProducer{Runner: "runner", Role: "gate"}
	raw, err := receipt.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Signature = &schema.SignatureBlock{Alg: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, preimage("", raw)))}
	raw, err = receipt.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(raw, pub, expectFor(r, "")); err == nil || !strings.Contains(err.Error(), "gate verdict") {
		t.Fatalf("authenticated false gate accepted: %v", err)
	}
}
