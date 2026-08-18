package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/joshft/correctful/internal/receipt"
	"github.com/joshft/correctful/internal/signing"
	"github.com/joshft/correctful/internal/strictjson"
	"github.com/joshft/correctful/schema"
)

// subcommand routes keygen/sign/verify/render. The main command — the one
// that builds and executes the change under review — deliberately has NO
// signing flag: any process that runs reviewed tests must never hold the
// private key, so signing is a separate invocation fed an already-produced
// receipt (see internal/signing).
func subcommand(name string) func([]string) error {
	switch name {
	case "keygen":
		return cmdKeygen
	case "sign":
		return cmdSign
	case "verify":
		return cmdVerify
	case "render":
		return cmdRender
	}
	return nil
}

func cmdKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	dir := fs.String("out", ".", "directory for the new keypair")
	fs.Parse(args)
	privPath, pubPath, err := signing.Keygen(*dir)
	if err != nil {
		return err
	}
	fmt.Printf("private key: %s (0600 — a CI secret; the probe step must never see it)\n", privPath)
	fmt.Printf("public key:  %s (pin this in the verify step)\n", pubPath)
	return nil
}

func cmdSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	in := fs.String("receipt", "", `unsigned receipt JSON (path, or "-" for stdin)`)
	keyPath := fs.String("key", "", "ed25519 private key (PKCS#8 PEM)")
	audience := fs.String("audience", "", `stable repository identity to bind, e.g. "github.com/org/repo" (empty binds none — weaker, stated)`)
	out := fs.String("out", "", "write the signed receipt here (default stdout)")
	fs.Parse(args)
	if *in == "" || *keyPath == "" {
		return fmt.Errorf("need -receipt and -key")
	}

	data, err := readArtifact(*in)
	if err != nil {
		return err
	}
	var r schema.Receipt
	if err := strictjson.Decode(data, &r); err != nil {
		return fmt.Errorf("parsing receipt: %w", err)
	}
	priv, err := signing.LoadPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	signed, err := signing.Sign(r, priv, *audience)
	if err != nil {
		return err
	}
	w := io.Writer(os.Stdout)
	if *out != "" {
		fh, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer fh.Close()
		w = fh
	}
	return receipt.WriteJSON(w, signed)
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	in := fs.String("receipt", "", "signed receipt JSON (path)")
	pubPath := fs.String("pub", "", "trusted ed25519 public key (PKIX PEM) — the trust root, pinned by the verifier, never taken from the receipt")
	head := fs.String("head", "", "expected head SHA of the change under review")
	base := fs.String("base", "", "expected base SHA (optional extra pin)")
	inputDigest := fs.String("input-digest", "", "expected input digest (optional extra pin)")
	audience := fs.String("audience", "", "expected audience the signature must be bound to")
	anySubject := fs.Bool("any-subject", false, "skip subject matching — authenticity only; states so in the output")
	gate := fs.Bool("gate", false, "after verifying, also exit 1 when the receipt's gate blocks")
	fs.Parse(args)
	if *in == "" || *pubPath == "" {
		return fmt.Errorf("need -receipt and -pub")
	}
	// A merge gate must pin the exact change, not just its head commit: one
	// head commit has many possible diffs (different bases, dirty overlays),
	// and a valid signature over SOME receipt at that head is not a valid
	// receipt for THIS change. So when -gate is set, require the base SHA
	// too (CI knows it — the merge base / $GITHUB_BASE_REF). -input-digest
	// is the strongest additional pin for a mid-branch or dirty tree.
	if *gate && !*anySubject && *base == "" {
		return fmt.Errorf("-gate requires -base (the head commit alone does not identify the exact diff); pass the merge base, or -any-subject to gate on authenticity only")
	}

	data, err := readArtifact(*in)
	if err != nil {
		return err
	}
	trusted, err := signing.LoadPublicKey(*pubPath)
	if err != nil {
		return err
	}
	r, err := signing.Verify(data, trusted, signing.Expect{
		Audience:    *audience,
		HeadSHA:     *head,
		BaseSHA:     *base,
		InputDigest: *inputDigest,
		AnySubject:  *anySubject,
	})
	if err != nil {
		return err
	}

	fmt.Printf("verified: ed25519 signature over canonical receipt content (schema %s)\n", r.SchemaVersion)
	if *anySubject {
		fmt.Println("subject: NOT CHECKED (-any-subject) — this proves authenticity, not relevance to any change")
	} else {
		fmt.Printf("subject: head %.12s matches\n", r.Change.HeadSHA)
	}
	if a := r.Signature.Audience; a != "" {
		fmt.Printf("audience: %q\n", a)
	}
	if r.GateBlocked() {
		fmt.Println("gate: blocked")
		if *gate {
			os.Exit(1)
		}
	} else {
		fmt.Println("gate: pass")
	}
	return nil
}

func cmdRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	in := fs.String("receipt", "", `receipt JSON (path, or "-" for stdin)`)
	format := fs.String("format", "md", "rendering: text or md")
	fs.Parse(args)
	if *in == "" {
		return fmt.Errorf("need -receipt")
	}
	data, err := readArtifact(*in)
	if err != nil {
		return err
	}
	var r schema.Receipt
	if err := strictjson.Decode(data, &r); err != nil {
		return fmt.Errorf("parsing receipt: %w", err)
	}
	switch *format {
	case "md":
		receipt.WriteMarkdown(os.Stdout, r)
	case "text":
		receipt.WriteText(os.Stdout, r)
	default:
		return fmt.Errorf("unknown -format %q (want text or md)", *format)
	}
	return nil
}

func readArtifact(path string) ([]byte, error) {
	var rd io.Reader
	if path == "-" {
		rd = os.Stdin
	} else {
		fh, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer fh.Close()
		rd = fh
	}
	data, err := io.ReadAll(io.LimitReader(rd, signing.MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > signing.MaxArtifactBytes {
		return nil, fmt.Errorf("receipt exceeds %d bytes", signing.MaxArtifactBytes)
	}
	return data, nil
}
