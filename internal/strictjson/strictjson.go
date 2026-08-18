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
	"unicode/utf8"
)

// Decode parses data into v under the strict contract. The stdlib decoder
// alone keeps a duplicate key's last value, matches struct fields
// case-insensitively (accepted: canonical producers emit exact names, and
// the signature layer separately requires byte-identical re-encoding), and
// — the trailing-content gap — Decoder.More reports false at a stray
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
