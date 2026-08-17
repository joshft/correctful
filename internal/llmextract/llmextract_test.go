package llmextract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/joshft/correctful/schema"
)

// samplePatch is a real-shaped two-file unified diff (git's exact section and
// hunk framing; content synthetic).
const samplePatch = `diff --git a/pkg/gate/gate.go b/pkg/gate/gate.go
index 1111111..2222222 100644
--- a/pkg/gate/gate.go
+++ b/pkg/gate/gate.go
@@ -10,6 +10,9 @@ func Check(x []byte) bool {
+	if x == nil {
+		return false
+	}
 	return len(x) > 0
 }
diff --git a/docs/notes.md b/docs/notes.md
index 3333333..4444444 100644
--- a/docs/notes.md
+++ b/docs/notes.md
@@ -1,2 +1,3 @@
 # Notes
+The gate now rejects nil.
`

// apiFixture wraps model output text in the real Messages API response shape
// (captured 2026-08-18; ids and model anonymized).
func apiFixture(text string) string {
	blob, _ := json.Marshal(text)
	return `{"id":"msg_01XFDUDYJgAACzvnptvVoYEL","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":` + string(blob) + `}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1200,"output_tokens":180}}`
}

// fixtureServer serves one canned response and records the request body and
// headers (readable after the call under test completes).
func fixtureServer(t *testing.T, status int, body string) (*Client, *[]byte, *http.Header) {
	t.Helper()
	var gotBody []byte
	var gotHeader http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &Client{APIKey: "test-key", Model: DefaultModel, BaseURL: srv.URL}, &gotBody, &gotHeader
}

// TestExtractMintsValidatedProposals: valid proposals become probe-less
// llm-proposed claims; a shape outside the taxonomy and a file outside the
// diff are REJECTED, never repaired. The request pins the contract: model,
// temperature 0, key header, and the diff itself in the prompt.
func TestExtractMintsValidatedProposals(t *testing.T) {
	out := `[
	  {"shape":"invariant","file":"pkg/gate/gate.go","text":"Check rejects nil input."},
	  {"shape":"opinion","file":"pkg/gate/gate.go","text":"the code is nice"},
	  {"shape":"assertion","file":"pkg/other/nope.go","text":"a file the diff never touched"},
	  {"shape":"assertion","file":"pkg/gate/gate.go","text":"Check still accepts non-empty input."}
	]`
	client, reqBody, reqHeader := fixtureServer(t, 200, apiFixture(out))
	h := Harvester{Patch: samplePatch, Client: client}

	res, err := h.Harvest("/nowhere", []string{"pkg/gate/gate.go", "docs/notes.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Claims) != 2 {
		t.Fatalf("claims = %+v, want 2 (invalid shape and unknown file rejected)", res.Claims)
	}
	first := res.Claims[0]
	if !regexp.MustCompile(`^LLM:pkg/gate/gate\.go:[0-9a-f]{8}$`).MatchString(first.ID) ||
		first.Shape != schema.ShapeInvariant ||
		first.Source.Kind != schema.SourceLLM || first.Text != "Check rejects nil input." {
		t.Errorf("first claim = %+v", first)
	}
	// The id is content-derived: the same proposal always mints the same id,
	// so re-runs and reorderings do not re-label claims.
	if first.ID != fmt.Sprintf("LLM:pkg/gate/gate.go:%08x", fnv32("pkg/gate/gate.go\x00Check rejects nil input.")) {
		t.Errorf("id %q is not the content hash", first.ID)
	}
	if len(first.ProbeIDs) != 0 {
		t.Errorf("llm proposal carries probes %v — a proposal must be remainder-bound", first.ProbeIDs)
	}
	if got := []string{res.Read[0], res.Read[1]}; got[0] != "pkg/gate/gate.go" || got[1] != "docs/notes.md" {
		t.Errorf("read = %v", res.Read)
	}

	var req apiRequest
	if err := json.Unmarshal(*reqBody, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != DefaultModel || req.Temperature != 0 {
		t.Errorf("request model/temp = %q/%v", req.Model, req.Temperature)
	}
	if !strings.Contains(req.Messages[0].Content, "diff --git a/pkg/gate/gate.go") {
		t.Error("prompt does not carry the diff")
	}
	if reqHeader.Get("x-api-key") != "test-key" || reqHeader.Get("anthropic-version") == "" {
		t.Errorf("auth headers = %v", *reqHeader)
	}
}

// TestFenceWrappedOutputTolerated: the one tolerated wrapper is a markdown
// code fence — everything else fails loudly.
func TestFenceWrappedOutputTolerated(t *testing.T) {
	out := "```json\n[{\"shape\":\"assertion\",\"file\":\"pkg/gate/gate.go\",\"text\":\"Check rejects nil.\"}]\n```"
	client, _, _ := fixtureServer(t, 200, apiFixture(out))
	res, err := Harvester{Patch: samplePatch, Client: client}.Harvest("", []string{"pkg/gate/gate.go"})
	if err != nil || len(res.Claims) != 1 {
		t.Fatalf("res=%+v err=%v, want 1 claim", res, err)
	}
}

// TestProseWrappedOutputFailsLoud: prose around the JSON is a broken contract
// and must surface as an error, not be silently salvaged.
func TestProseWrappedOutputFailsLoud(t *testing.T) {
	out := "Here are the key claims I found:\n[]"
	client, _, _ := fixtureServer(t, 200, apiFixture(out))
	_, err := Harvester{Patch: samplePatch, Client: client}.Harvest("", []string{"pkg/gate/gate.go"})
	if err == nil || !strings.Contains(err.Error(), "not the required JSON array") {
		t.Fatalf("err = %v, want loud parse failure", err)
	}
}

// TestAPIErrorSurfaces: an API-level error is reported with its type and
// message, loudly.
func TestAPIErrorSurfaces(t *testing.T) {
	body := `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	client, _, _ := fixtureServer(t, 401, body)
	_, err := Harvester{Patch: samplePatch, Client: client}.Harvest("", []string{"pkg/gate/gate.go"})
	if err == nil || !strings.Contains(err.Error(), "authentication_error") {
		t.Fatalf("err = %v, want surfaced API error", err)
	}
}

// TestByteCapIsHardAndDisclosed: the cap is enforced two ways, both disclosed
// through Read. A single section LARGER than the whole cap is skipped
// entirely — sent whole or not at all, never truncated mid-hunk — and once
// the cumulative cap is reached, later sections are cut.
func TestByteCapIsHardAndDisclosed(t *testing.T) {
	oversized := strings.Repeat("+padding line to blow past the byte cap\n", (maxPatchBytes/40)+10)
	section := func(name, body string) string {
		return "diff --git a/" + name + " b/" + name + "\n--- a/" + name + "\n+++ b/" + name + "\n@@ -0,0 +1 @@\n" + body
	}

	// An oversized FIRST section must not smuggle itself in by being first.
	patch := section("first.go", oversized) + section("second.go", "+small\n")
	client, reqBody, _ := fixtureServer(t, 200, apiFixture(`[]`))
	res, err := Harvester{Patch: patch, Client: client}.Harvest("", []string{"first.go", "second.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Read) != 1 || res.Read[0] != "second.go" {
		t.Fatalf("read = %v, want only second.go (oversized first section skipped whole)", res.Read)
	}
	var req apiRequest
	if err := json.Unmarshal(*reqBody, &req); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req.Messages[0].Content, "first.go") {
		t.Error("prompt contains the oversized section the hard cap should have skipped")
	}

	// Two half-cap sections: the second crosses the cumulative cap and is cut.
	half := strings.Repeat("+p\n", maxPatchBytes/6)
	patch2 := section("a.go", half) + section("b.go", half)
	client2, _, _ := fixtureServer(t, 200, apiFixture(`[]`))
	res2, err := Harvester{Patch: patch2, Client: client2}.Harvest("", []string{"a.go", "b.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Read) != 1 || res2.Read[0] != "a.go" {
		t.Fatalf("read = %v, want only a.go (cumulative cap cut b.go)", res2.Read)
	}
}

// TestContextCancellationPropagates: the receipt's budget reaches the API
// call — a cancelled context aborts the extraction with an error instead of
// hanging on a background context.
func TestContextCancellationPropagates(t *testing.T) {
	client, _, _ := fixtureServer(t, 200, apiFixture(`[]`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Harvester{Ctx: ctx, Patch: samplePatch, Client: client}.Harvest("", []string{"pkg/gate/gate.go"})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v, want context cancellation", err)
	}
}

// TestSectionFileParsing: post-image paths come from the section header.
func TestSectionFileParsing(t *testing.T) {
	secs := splitSections(samplePatch)
	if len(secs) != 2 {
		t.Fatalf("sections = %d, want 2", len(secs))
	}
	if f := sectionFile(secs[0]); f != "pkg/gate/gate.go" {
		t.Errorf("section 0 file = %q", f)
	}
	if f := sectionFile(secs[1]); f != "docs/notes.md" {
		t.Errorf("section 1 file = %q", f)
	}
}
