package receipt

import (
	"bytes"
	"encoding/json"

	"github.com/joshft/correctful/schema"
)

// Canonical serializes a receipt into its one canonical byte form: the
// two-space-indented encoding WriteJSON has always emitted, trailing
// newline included. The signature layer signs and verifies exactly these
// bytes (with the signature block absent), and the verifier additionally
// requires a signed artifact to BE its own canonical form byte-for-byte —
// so there is no room for a normalization differential where one parser
// reads a receipt one way and this tool another. Every encoder property is
// therefore load-bearing and frozen by golden-vector test: struct-order
// keys, sorted map keys (encoding/json sorts them), HTML-escaped <>&, and
// the indent.
func Canonical(r schema.Receipt) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
