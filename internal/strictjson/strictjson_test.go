package strictjson

import (
	"strings"
	"testing"
)

type doc struct {
	A string `json:"a"`
	B []int  `json:"b,omitempty"`
}

// TestCleanDocumentDecodes: the strict contract still admits an ordinary
// document.
func TestCleanDocumentDecodes(t *testing.T) {
	var d doc
	if err := Decode([]byte(`{"a":"x","b":[1,2]}`), &d); err != nil {
		t.Fatalf("clean document rejected: %v", err)
	}
	if d.A != "x" || len(d.B) != 2 {
		t.Fatalf("decoded wrong values: %+v", d)
	}
}

// TestStrictRejections: every leniency the stdlib decoder would allow is
// refused. The stray-closing-delimiter case is the regression pin for the
// Decoder.More gap: More() reports false at "]", so "{...}]" passed the
// old intake check.
func TestStrictRejections(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"duplicate key", `{"a":"x","a":"y"}`, "duplicate key"},
		{"duplicate key nested", `{"a":"x","b":[{"c":1,"c":2}]}`, "duplicate key"},
		{"case-variant key collision", `{"a":"x","A":"y"}`, "case-variant key collision"},
		{"unknown field", `{"a":"x","zz":1}`, "unknown field"},
		{"trailing value", `{"a":"x"} true`, "trailing content"},
		{"stray closing delimiter", `{"a":"x"}]`, "trailing content"},
		{"stray closing brace", `{"a":"x"}}`, "trailing content"},
		{"invalid utf-8", "{\"a\":\"\xff\"}", "not valid UTF-8"},
	}
	for _, c := range cases {
		var d doc
		err := Decode([]byte(c.in), &d)
		if err == nil {
			t.Fatalf("%s: accepted %q", c.name, c.in)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %q does not name %q", c.name, err, c.want)
		}
	}
}
