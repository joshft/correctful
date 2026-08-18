package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// maxKeyFileBytes bounds a key file read — a PEM Ed25519 key is a few
// hundred bytes; anything near the cap is not a key.
const maxKeyFileBytes = 16 << 10

// Keygen mints an Ed25519 keypair into dir as correctful.key (PKCS#8 PEM,
// 0600) and correctful.pub (PKIX PEM, 0644). Both files are created
// exclusively — an existing file or a pre-planted symlink at either path
// fails the whole operation, and a failed public write removes the private
// file so no partial pair survives.
func Keygen(dir string) (privPath, pubPath string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	privPath = filepath.Join(dir, "correctful.key")
	pubPath = filepath.Join(dir, "correctful.pub")

	if err := writeExclusive(privPath, pemBytes("PRIVATE KEY", privDER), 0o600); err != nil {
		return "", "", err
	}
	if err := writeExclusive(pubPath, pemBytes("PUBLIC KEY", pubDER), 0o644); err != nil {
		os.Remove(privPath)
		return "", "", err
	}
	return privPath, pubPath, nil
}

func pemBytes(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	_, werr := fh.Write(data)
	cerr := fh.Close()
	if werr != nil {
		os.Remove(path)
		return werr
	}
	if cerr != nil {
		os.Remove(path)
		return cerr
	}
	return nil
}

// LoadPrivateKey reads exactly one PKCS#8 PEM block holding an Ed25519
// private key. Trailing data, a second block, or any other key type fails —
// a key file that is not precisely what it claims is an operator error
// worth stopping on. The path may be a symlink on purpose: CI secret
// mounts are symlink farms, and the key file is operator-owned trusted
// input, not attacker-controlled data.
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	der, err := readSinglePEM(path, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s holds a %T, not an ed25519 private key", path, k)
	}
	return priv, nil
}

// LoadPublicKey reads exactly one PKIX PEM block holding an Ed25519 public
// key, under the same single-block strictness as LoadPrivateKey.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	der, err := readSinglePEM(path, "PUBLIC KEY")
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%s holds a %T, not an ed25519 public key", path, k)
	}
	return pub, nil
}

func readSinglePEM(path, wantType string) ([]byte, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, maxKeyFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxKeyFileBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes — not a key file", path, maxKeyFileBytes)
	}
	block, rest := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s holds no PEM block", path)
	}
	if block.Type != wantType {
		return nil, fmt.Errorf("%s holds a %q block, want %q", path, block.Type, wantType)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%s carries %d bytes after the PEM block — one key per file", path, len(rest))
	}
	return block.Bytes, nil
}
