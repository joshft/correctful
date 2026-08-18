// Package strictjson decodes JSON under the strictest contract the stdlib
// can be driven to: unknown fields rejected, duplicate keys rejected at any
// depth, trailing content rejected down to the last byte, and invalid UTF-8
// rejected instead of silently normalized.
//
// Two callers depend on this being airtight. The intake boundary parses
// supplier documents whose duplicate keys were demonstrated to smuggle a
// second "outcome": "verified" behind a "counterexample". The signature
// verifier parses receipts where ANY parser leniency is a differential: a
// document this package accepts but re-serializes differently than it
// arrived is a document two consumers can read two ways.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Decode parses data into v under the strict contract. The stdlib decoder
// alone keeps a duplicate key's last value, matches struct fields
// case-insensitively (so a case-variant sibling key silently overrides — see
// rejectDupKeysIn, which rejects the collision), and — the trailing-content
// gap — Decoder.More reports false at a stray
// closing delimiter, so "{...}]" passes a More-based check. Decode demands
// io.EOF from the token stream instead.
func Decode(data []byte, v any) error {
	if !utf8.Valid(data) {
		return errors.New("document is not valid UTF-8")
	}
	if err := rejectDupKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing content after the JSON document")
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
		var seen []string
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			k, _ := kt.(string)
			// Exact duplicates are the obvious smuggle. Case-fold
			// collisions are the subtle one: encoding/json matches a JSON
			// key to a struct field case-INsensitively and lets a later
			// key win, so {"outcome":"counterexample","Outcome":"verified"}
			// decodes to "verified" while our exact-match check saw two
			// distinct keys. Demonstrated live to turn an intake
			// counterexample into a pass. Honest producers never emit two
			// keys equal under case folding, so rejecting the collision
			// costs nothing and closes the differential at the source.
			for _, prev := range seen {
				if prev == k {
					return fmt.Errorf("duplicate key %q", k)
				}
				if strings.EqualFold(prev, k) {
					return fmt.Errorf("case-variant key collision: %q and %q decode to one field", prev, k)
				}
			}
			seen = append(seen, k)
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
