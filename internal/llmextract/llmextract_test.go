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
// key header, and the diff itself in the prompt (temperature is omitted —
// the API deprecated it for this model generation; fail-loud caught that
// live).
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
	if req.Model != DefaultModel {
		t.Errorf("request model = %q", req.Model)
	}
	if strings.Contains(string(*reqBody), "temperature") {
		t.Error("request carries temperature — deprecated for this model generation")
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

// TestDotDirSectionsNeverReachTheModel: hidden-directory sections (installed
// tooling, methodology documents) are excluded from the prompt BEFORE the
// byte cap is spent — measured live, dot-dir documents sorted first in a real
// diff and consumed the entire cap, so the model never saw shipped code.
func TestDotDirSectionsNeverReachTheModel(t *testing.T) {
	patch := "diff --git a/.tooling/ARCH.md b/.tooling/ARCH.md\n--- a/.tooling/ARCH.md\n+++ b/.tooling/ARCH.md\n@@ -0,0 +1 @@\n+### INV-001: docs about the change\n" +
		"diff --git a/pkg/gate.go b/pkg/gate.go\n--- a/pkg/gate.go\n+++ b/pkg/gate.go\n@@ -0,0 +1 @@\n+func Gate() {}\n"
	client, reqBody, _ := fixtureServer(t, 200, apiFixture(`[]`))
	res, err := Harvester{Patch: patch, Client: client}.Harvest("", []string{".tooling/ARCH.md", "pkg/gate.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Read) != 1 || res.Read[0] != "pkg/gate.go" {
		t.Fatalf("read = %v, want only the shipped-code section", res.Read)
	}
	if strings.Contains(string(*reqBody), ".tooling/ARCH.md") {
		t.Error("prompt carries a dot-dir section")
	}
}

// TestGithubSectionsIncludedAfterCode: .github is project-owned behavior (the
// repository's own CI contract), not installed tooling — its sections reach
// the model, but ONLY after every non-hidden section, so they can never
// crowd shipped code out of the byte cap. Other dot-dirs stay excluded.
func TestGithubSectionsIncludedAfterCode(t *testing.T) {
	patch := "diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml\n--- a/.github/workflows/ci.yml\n+++ b/.github/workflows/ci.yml\n@@ -0,0 +1 @@\n+      continue-on-error: true\n" +
		"diff --git a/.tooling/ARCH.md b/.tooling/ARCH.md\n--- a/.tooling/ARCH.md\n+++ b/.tooling/ARCH.md\n@@ -0,0 +1 @@\n+### INV-001: docs\n" +
		"diff --git a/pkg/gate.go b/pkg/gate.go\n--- a/pkg/gate.go\n+++ b/pkg/gate.go\n@@ -0,0 +1 @@\n+func Gate() {}\n"
	client, reqBody, _ := fixtureServer(t, 200, apiFixture(`[]`))
	res, err := Harvester{Patch: patch, Client: client}.Harvest("", []string{".github/workflows/ci.yml", ".tooling/ARCH.md", "pkg/gate.go"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(*reqBody)
	codeAt := strings.Index(body, "pkg/gate.go")
	ghAt := strings.Index(body, ".github/workflows/ci.yml")
	if codeAt < 0 || ghAt < 0 {
		t.Fatalf("prompt missing sections (code at %d, .github at %d):\n%.400s", codeAt, ghAt, body)
	}
	if ghAt < codeAt {
		t.Error(".github section precedes shipped code — ordering must keep code first")
	}
	if strings.Contains(body, ".tooling/ARCH.md") {
		t.Error("a non-.github dot-dir section reached the model")
	}
	if len(res.Read) != 2 {
		t.Errorf("read = %v, want the code and .github sections", res.Read)
	}
}

// TestGithubSectionCannotCrowdOutCode: when the cap is nearly spent by
// shipped-code sections, the .github section is the one that gets dropped —
// the ordering is load-bearing, not cosmetic.
func TestGithubSectionCannotCrowdOutCode(t *testing.T) {
	line := strings.Repeat("x", 1024)
	var code strings.Builder
	for i := 0; code.Len() < maxPatchBytes-2048; i++ {
		code.WriteString("diff --git a/pkg/f" + string(rune('a'+i%26)) + ".go b/pkg/f" + string(rune('a'+i%26)) + ".go\n@@ -0,0 +1 @@\n+// " + line + "\n")
	}
	gh := "diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml\n@@ -0,0 +1 @@\n+" + strings.Repeat("y", 4096) + "\n"
	// .github FIRST in the diff — the ordering must still put it last and
	// drop it at the cap.
	sections, _ := includedSections(gh+code.String(), nil)
	joined := strings.Join(sections, "")
	if strings.Contains(joined, ".github/workflows/ci.yml") {
		t.Error(".github section crowded shipped code at the cap boundary")
	}
	if !strings.Contains(joined, "pkg/f") {
		t.Error("shipped-code sections missing")
	}
}
