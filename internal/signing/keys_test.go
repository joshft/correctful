package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshft/correctful/internal/receipt"
)

// TestKeygenAndLoadRoundTrip: mint, load both halves, and prove they are
// one pair by signing and verifying through the real API. The private file
// must be 0600.
func TestKeygenAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath, err := Keygen(dir)
	if err != nil {
		t.Fatalf("Keygen: %v", err)
	}
	fi, err := os.Stat(privPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("private key mode %v, want 0600", fi.Mode().Perm())
	}
	priv, err := LoadPrivateKey(privPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	pub, err := LoadPublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadPublicKey: %v", err)
	}
	signed, err := Sign(fixtureReceipt(t), priv, "")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := receipt.Canonical(signed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(artifact, pub, Expect{AnySubject: true}); err != nil {
		t.Fatalf("minted pair does not verify its own signature: %v", err)
	}
}

// TestKeygenRefusesExistingAndLeavesNoPartialPair: an existing file at
// either path fails the whole operation, and a public-half failure removes
// the already-written private half.
func TestKeygenRefusesExistingAndLeavesNoPartialPair(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Keygen(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Keygen(dir); err == nil {
		t.Fatalf("second Keygen over an existing pair succeeded")
	}

	dir2 := t.TempDir()
	// Pre-plant the PUBLIC path so the private write succeeds and the
	// public write fails: no partial pair may survive.
	if err := os.WriteFile(filepath.Join(dir2, "correctful.pub"), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Keygen(dir2); err == nil {
		t.Fatalf("Keygen succeeded over an occupied public path")
	}
	if _, err := os.Lstat(filepath.Join(dir2, "correctful.key")); !os.IsNotExist(err) {
		t.Errorf("private key left behind after failed pair: %v", err)
	}
}

// TestKeygenRefusesPlantedSymlink: O_EXCL|O_NOFOLLOW means a pre-created
// symlink cannot redirect either output.
func TestKeygenRefusesPlantedSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(dir, "correctful.key")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Keygen(dir); err == nil {
		t.Fatalf("Keygen followed a planted symlink")
	}
	if _, err := os.Lstat(filepath.Join(dir, "elsewhere")); !os.IsNotExist(err) {
		t.Errorf("symlink target was created: %v", err)
	}
}

// TestLoadKeyRejections: a key file must be exactly one PEM block of
// exactly the right type holding exactly an Ed25519 key — RSA keys,
// trailing bytes, second blocks, and non-PEM garbage all fail loudly.
func TestLoadKeyRejections(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER})

	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	edDER, _ := x509.MarshalPKCS8PrivateKey(edPriv)
	edPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: edDER})

	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"rsa key", rsaPEM, "not an ed25519"},
		{"trailing data", append(append([]byte{}, edPEM...), "extra\n"...), "after the PEM block"},
		{"two blocks", append(append([]byte{}, edPEM...), edPEM...), "after the PEM block"},
		{"wrong block type", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: edDER}), `want "PRIVATE KEY"`},
		{"garbage", []byte("not a pem at all"), "no PEM block"},
	}
	for _, c := range cases {
		p := write(strings.ReplaceAll(c.name, " ", "-"), c.data)
		_, err := LoadPrivateKey(p)
		if err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %q does not name %q", c.name, err, c.want)
		}
	}

	// A private key where a public one is expected fails on block type.
	privPath := write("priv-as-pub", edPEM)
	if _, err := LoadPublicKey(privPath); err == nil || !strings.Contains(err.Error(), `want "PUBLIC KEY"`) {
		t.Fatalf("private key accepted as public: %v", err)
	}
}

// TestKeygenRefusesSymlinkedParentDir: O_NOFOLLOW on the final filename is
// not enough — a symlinked OUTPUT DIRECTORY would redirect both writes.
// Keygen opens the directory with O_NOFOLLOW and creates the files relative
// to that descriptor, so a symlinked -out dir is refused.
func TestKeygenRefusesSymlinkedParentDir(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Keygen(link); err == nil {
		t.Fatalf("Keygen wrote into a symlinked output directory")
	}
	if _, err := os.Lstat(filepath.Join(real, "correctful.key")); !os.IsNotExist(err) {
		t.Errorf("a key was created through the symlinked directory: %v", err)
	}
}
