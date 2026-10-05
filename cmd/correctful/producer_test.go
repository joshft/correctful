package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/internal/receipt"
	"github.com/joshft/correctful/internal/signing"
	"github.com/joshft/correctful/schema"
)

func TestSignCommandUsesProtectedProducerArguments(t *testing.T) {
	dir := t.TempDir()
	priv, pub, err := signing.Keygen(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := receipt.Assemble(gitdiff.Change{Repo: "example", HeadSHA: strings.Repeat("a", 40), Files: []string{"example.go"}}, nil, nil, schema.Coverage{Files: []schema.FileCoverage{{File: "example.go"}}, Unread: 1})
	in, out := filepath.Join(dir, "unsigned.json"), filepath.Join(dir, "signed.json")
	raw, err := receipt.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-receipt", in, "-key", priv, "-out", out}
	if err := cmdSign(args); err == nil {
		t.Fatal("current receipt signed without invoker configuration")
	}
	args = append(args, "-runner", "protected-runner", "-role", "gate")
	if err := cmdSign(args); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	key, err := signing.LoadPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := signing.Verify(raw, key, signing.Expect{HeadSHA: r.Change.HeadSHA, Runner: "protected-runner", Role: "gate"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Gate != "pass" {
		t.Fatal("CLI lost the computed verdict")
	}
	r.Producer = &schema.ReceiptProducer{Runner: "protected-runner", Role: "gate"}
	raw, err = receipt.Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdSign(args); err == nil || !strings.Contains(err.Error(), "must not select") {
		t.Fatalf("producer-supplied authority accepted: %v", err)
	}
}
