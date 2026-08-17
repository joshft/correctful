// Package llmextract is the v0.7 extractor: a language model reads the DIFF
// (never the whole repository) and proposes the claims the change makes
// implicitly — the wild case, where nobody wrote a test name, a spec id, or a
// MUST clause for the checker to harvest.
//
// The cardinal rule is structural here, not aspirational: proposed claims are
// minted with NO probes, so they land in the receipt's remainder at T0 and
// nothing the model says can raise a tier or produce a false pass. The worst
// a hallucinated claim can do is waste a remainder row, and every proposal is
// marked llm-proposed so a reader never mistakes it for something the change
// wrote down itself.
//
// Determinism discipline: pinned model (overridable via CORRECTFUL_LLM_MODEL),
// a strict JSON output contract with schema validation, and
// proposals rejected — not repaired — when they name a file outside the diff
// or a shape outside the taxonomy.
package llmextract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joshft/correctful/internal/harvest"
	"github.com/joshft/correctful/schema"
)

// DefaultModel is the pinned extraction model. Claim extraction is a reading
// task, not a reasoning marathon — the mid-tier model is the right default.
const DefaultModel = "claude-sonnet-5"

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	maxProposals   = 20
	// maxOutputTokens leaves room for the model's reasoning: this model
	// generation reasons before answering (measured live — 4096 truncated the
	// answer mid-array, and the parse gate caught it), so the budget covers
	// thinking plus the full proposal array.
	maxOutputTokens = 16384
	// maxPatchBytes bounds what the model reads. Truncation is DISCLOSED, not
	// silent: only files whose diff sections were actually sent are reported
	// as read, so the receipt's coverage shows exactly what the extractor saw.
	maxPatchBytes = 150 * 1024
)

// Client is a minimal Anthropic Messages API client — stdlib only.
type Client struct {
	APIKey  string
	Model   string
	BaseURL string // defaults to the public API; tests point it at a fixture server
	HTTP    *http.Client
}

// NewClient builds a client from the environment: ANTHROPIC_API_KEY
// (required) and CORRECTFUL_LLM_MODEL (optional model override).
func NewClient() (*Client, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("llm extraction requested but ANTHROPIC_API_KEY is not set")
	}
	model := os.Getenv("CORRECTFUL_LLM_MODEL")
	if model == "" {
		model = DefaultModel
	}
	return &Client{APIKey: key, Model: model}, nil
}

// Harvester adapts one extraction over a unified diff to the harvest
// interface, so coverage accounting and claim reconciliation apply to LLM
// proposals exactly as they do to every mechanical harvester. Ctx carries the
// receipt's overall budget into the API call — the harvest interface is
// context-free, so the deadline rides in the struct.
type Harvester struct {
	Ctx    context.Context
	Patch  string
	Client *Client
}

func (Harvester) Name() string { return "llm" }

func (h Harvester) Harvest(repoDir string, files []string) (harvest.Result, error) {
	sent, read := includedSections(h.Patch, files)
	if len(sent) == 0 {
		return harvest.Result{}, nil
	}
	ctx := h.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := h.Client.complete(ctx, extractionPrompt(sent))
	if err != nil {
		return harvest.Result{}, fmt.Errorf("llm extraction: %w", err)
	}
	claims, err := mintProposals(raw, read)
	if err != nil {
		return harvest.Result{}, fmt.Errorf("llm extraction: %w", err)
	}
	return harvest.Result{Claims: claims, Read: read}, nil
}

// includedSections splits a unified diff on file boundaries and keeps whole
// sections, in order, until the byte cap. It returns the kept sections and
// the repo-relative files they cover (restricted to the change's file set).
func includedSections(patch string, files []string) (sections []string, read []string) {
	inChange := make(map[string]bool, len(files))
	for _, f := range files {
		inChange[f] = true
	}
	total := 0
	for _, sec := range splitSections(patch) {
		if harvest.UnderDotDir(sectionFile(sec)) {
			// The same principle every mechanical harvester applies: hidden
			// directories hold installed tooling and its documentation, not
			// the change's code. Measured live: without this, a change's
			// dot-dir methodology documents sorted first in the diff and
			// consumed the ENTIRE byte cap — the model never saw a line of
			// shipped code and re-extracted the docs' claims instead
			// (extraction-over-prose, the class the cap exists to feed code
			// into, not documents).
			continue
		}
		if len(sec) > maxPatchBytes {
			continue // a section is sent whole or not at all — never truncated mid-hunk
		}
		if total+len(sec) > maxPatchBytes {
			break // cap reached; later files stay un-read and coverage says so
		}
		total += len(sec)
		sections = append(sections, sec)
		if f := sectionFile(sec); f != "" && inChange[f] {
			read = append(read, f)
		}
	}
	return sections, read
}

// splitSections cuts a unified diff at "diff --git" boundaries.
func splitSections(patch string) []string {
	var out []string
	for _, part := range strings.Split("\n"+patch, "\ndiff --git ") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, "diff --git "+part)
	}
	return out
}

// sectionFile extracts the post-image path from a section header
// ("diff --git a/x b/x" -> "x").
func sectionFile(sec string) string {
	header, _, _ := strings.Cut(sec, "\n")
	if i := strings.LastIndex(header, " b/"); i >= 0 {
		return strings.TrimSpace(header[i+len(" b/"):])
	}
	return ""
}

// proposal is the model's required output element.
type proposal struct {
	Shape string `json:"shape"`
	File  string `json:"file"`
	Text  string `json:"text"`
}

// allowedShapes is the claim taxonomy the model may use — anything else is
// rejected, not coerced.
var allowedShapes = map[string]schema.Shape{
	"assertion":               schema.ShapeAssertion,
	"invariant":               schema.ShapeInvariant,
	"must-clause":             schema.ShapeMustClause,
	"coupled-fields-lockstep": schema.ShapeCoupledFields,
	"safety-assert":           schema.ShapeSafetyAssert,
	"witness":                 schema.ShapeWitness,
}

func extractionPrompt(sections []string) string {
	var b strings.Builder
	b.WriteString(`You are the claim-extraction stage of correctful, a diff-level evidence checker. Read the unified diff below and list the CLAIMS the change makes: concrete, falsifiable assertions about behavior or properties that the change asserts to be true — by its code's construction, its naming, its comments, or its documented intent.

You are not reviewing the code. Do not suggest improvements, judge quality, or describe what the change does mechanically. State only what it CLAIMS.

Rules:
1. Each claim must be checkable in principle by a mechanical probe (a test, a property check, an observation).
2. Write each claim as one plain declarative sentence about the changed code.
3. Attribute each claim to the single changed file it is most about, using the exact path shown in the diff.
4. Propose at most ` + fmt.Sprint(maxProposals) + ` claims — the most load-bearing ones. If the diff supports fewer, return fewer. Do not invent claims the diff does not support.

Output: a JSON array only — no prose, no code fences. Each element:
{"shape": "<one of: assertion, invariant, must-clause, coupled-fields-lockstep, safety-assert, witness>", "file": "<changed file path>", "text": "<one sentence>"}

The diff:

`)
	for _, s := range sections {
		b.WriteString(s)
		if !strings.HasSuffix(s, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// mintProposals validates the model's output and mints probe-less claims.
// Malformed output fails LOUDLY — a parse gate, not a salvage operation; the
// only tolerated wrapper is a markdown code fence.
func mintProposals(raw string, readFiles []string) ([]schema.Claim, error) {
	inRead := make(map[string]bool, len(readFiles))
	for _, f := range readFiles {
		inRead[f] = true
	}
	var props []proposal
	if err := json.Unmarshal([]byte(stripFences(raw)), &props); err != nil {
		return nil, fmt.Errorf("model output is not the required JSON array: %v (output begins %q)", err, truncateStr(raw, 120))
	}

	var claims []schema.Claim
	for _, p := range props {
		shape, ok := allowedShapes[strings.TrimSpace(p.Shape)]
		text := strings.TrimSpace(p.Text)
		if !ok || text == "" || !inRead[p.File] {
			continue // outside the taxonomy or the diff: rejected, never repaired
		}
		if len(claims) == maxProposals {
			break
		}
		claims = append(claims, schema.Claim{
			// The id hashes the proposal's content, so it is stable across
			// reorderings and re-runs of the same proposal — an ordinal would
			// re-label every claim whenever the model reordered its output.
			ID:    fmt.Sprintf("LLM:%s:%08x", p.File, fnv32(p.File+"\x00"+text)),
			Shape: shape,
			Text:  truncateStr(text, 300),
			Source: schema.Source{
				Kind: schema.SourceLLM,
				File: p.File,
				Ref:  "llm",
			},
			// No probes, by design: a proposal is remainder-bound until a
			// machine verifies it.
		})
	}
	return claims, nil
}

// fnv32 is FNV-1a over the string — a stable content id, not a security hash.
func fnv32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// stripFences removes a wrapping markdown code fence if present.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- Anthropic Messages API (stdlib only) ---

type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []apiMessage `json:"messages"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// complete sends one user message and returns the concatenated text blocks.
func (c *Client) complete(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(apiRequest{
		Model:     c.Model,
		MaxTokens: maxOutputTokens,
		Messages:  []apiMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", apiVersion)

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var parsed apiResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("API response unparseable (HTTP %d): %v", resp.StatusCode, err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("API error (HTTP %d): %s: %s", resp.StatusCode, parsed.Error.Type, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API HTTP %d: %s", resp.StatusCode, truncateStr(string(data), 200))
	}
	if parsed.StopReason == "max_tokens" {
		return "", fmt.Errorf("model output truncated at the %d-token budget (stop_reason max_tokens)", maxOutputTokens)
	}
	var out strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}
