// Command correctful reads a change, harvests the claims it makes, dispatches
// probes to test them, and emits a receipt — including the honest unverified
// remainder.
//
// Usage:
//
//	correctful [flags]
//
//	-base ref     diff against this ref (per-change receipt). "auto" detects
//	              the base ($GITHUB_BASE_REF in PR CI, else the default
//	              branch). Empty = whole working tree (the sweep organ).
//	              Default: empty.
//	-repo dir     repository to inspect. Default: current directory.
//	-format f     receipt format: text (terminal), json (the payload), or
//	              md (a pull-request comment). Default: text.
//	-json         deprecated alias for -format json.
//	-concurrency  max probes to run at once. Default: 4.
//	-timeout      overall probe budget. Default: 5m.
//
// Exit status: 0 when no claim was refuted; 1 when a probe ran and a claim did
// not hold (merge-gate semantics). The remainder never fails the run — it is an
// honest report, not a defect.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/internal/harvest"
	"github.com/joshft/correctful/internal/llmextract"
	"github.com/joshft/correctful/internal/probe"
	"github.com/joshft/correctful/internal/receipt"
)

func main() {
	base := flag.String("base", "", `diff against this ref; "auto" detects it; empty = whole working tree`)
	repo := flag.String("repo", ".", "repository directory to inspect")
	format := flag.String("format", "text", "receipt format: text, json, or md")
	asJSON := flag.Bool("json", false, "deprecated alias for -format json")
	concurrency := flag.Int("concurrency", 4, "max probes to run at once")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall probe budget")
	useLLM := flag.Bool("llm", false, "additionally PROPOSE claims from the diff with an LLM (needs ANTHROPIC_API_KEY; proposals are unverified and land in the remainder)")
	flag.Parse()

	if *asJSON {
		*format = "json"
	}
	if err := run(*base, *repo, *format, *concurrency, *timeout, *useLLM); err != nil {
		fmt.Fprintln(os.Stderr, "correctful:", err)
		os.Exit(2)
	}
}

func run(base, repo, format string, concurrency int, timeout time.Duration, useLLM bool) error {
	switch format {
	case "text", "json", "md":
	default:
		return fmt.Errorf("unknown -format %q (want text, json, or md)", format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Resolve the change: a diff against base (detected on "auto"), or the
	// whole tree.
	var (
		change gitdiff.Change
		err    error
	)
	if base == "auto" {
		base, err = gitdiff.DetectBase(ctx, repo)
		if err != nil {
			return fmt.Errorf("detecting base: %w", err)
		}
	}
	if base == "" {
		change, err = gitdiff.ResolveAll(ctx, repo)
	} else {
		change, err = gitdiff.Resolve(ctx, repo, base)
	}
	if err != nil {
		return fmt.Errorf("resolving change: %w", err)
	}

	root := change.Repo
	if root == "" {
		root = repo
	}
	// Pin the harvested input: commit SHAs identify only committed state, and
	// a mid-branch receipt harvests the working tree.
	change.InputDigest = gitdiff.InputDigest(root, change.Files)

	// Harvest claims, then dispatch probes against them.
	harvesters := harvest.Default()
	if useLLM {
		if base == "" {
			return fmt.Errorf("-llm needs a per-change receipt: pass -base (the extractor reads the diff, and a sweep has none)")
		}
		client, err := llmextract.NewClient()
		if err != nil {
			return err
		}
		patch, err := gitdiff.Patch(ctx, root, base)
		if err != nil {
			return fmt.Errorf("reading diff for llm extraction: %w", err)
		}
		harvesters = append(harvesters, llmextract.Harvester{Ctx: ctx, Patch: patch, Client: client})
	}
	claims, coverage, err := harvest.Run(root, change.Files, harvesters...)
	if err != nil {
		return fmt.Errorf("harvesting claims: %w", err)
	}

	// Anchor spec-id claims against the repo's definition corpus — the whole
	// tracked document set, not just the changed files, because the invariant
	// a changed test names is defined in an unchanged spec.
	docs, err := gitdiff.TrackedByPattern(ctx, root, "*.md", "*.markdown")
	if err != nil {
		return fmt.Errorf("listing definition corpus: %w", err)
	}
	var mentions int
	claims, mentions = harvest.AnchorClaims(claims, harvest.BuildDefIndex(root, docs), change.Files)
	coverage.SuppressedMentions = mentions

	evidence := probe.NewDispatcher(concurrency, probe.Default()...).
		Dispatch(ctx, root, claims)

	r := receipt.Assemble(change, claims, evidence, coverage)

	switch format {
	case "json":
		if err := receipt.WriteJSON(os.Stdout, r); err != nil {
			return err
		}
	case "md":
		receipt.WriteMarkdown(os.Stdout, r)
	default:
		receipt.WriteText(os.Stdout, r)
	}

	if r.Summary.Refuted > 0 {
		os.Exit(1)
	}
	return nil
}
