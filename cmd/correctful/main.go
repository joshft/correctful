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
// Exit status: 0 when the gate passes; 1 on a refutation, a policy miss, or a
// required intake supplier with no admitted document (merge-gate semantics —
// schema.Receipt.GateBlocked is the definition). The remainder never fails
// the run — it is an honest report, not a defect.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/joshft/correctful/internal/gitdiff"
	"github.com/joshft/correctful/internal/harvest"
	"github.com/joshft/correctful/internal/intake"
	"github.com/joshft/correctful/internal/llmextract"
	"github.com/joshft/correctful/internal/policy"
	"github.com/joshft/correctful/internal/probe"
	"github.com/joshft/correctful/internal/receipt"
	"github.com/joshft/correctful/schema"
)

func main() {
	base := flag.String("base", "", `diff against this ref; "auto" detects it; empty = whole working tree`)
	repo := flag.String("repo", ".", "repository directory to inspect")
	format := flag.String("format", "text", "receipt format: text, json, or md")
	asJSON := flag.Bool("json", false, "deprecated alias for -format json")
	concurrency := flag.Int("concurrency", 4, "max probes to run at once")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall probe budget")
	useLLM := flag.Bool("llm", false, "additionally PROPOSE claims from the diff with an LLM (needs ANTHROPIC_API_KEY; proposals are unverified and land in the remainder)")
	intakePath := flag.String("intake", "", "invoker-owned intake config admitting evidence from EXTERNAL suppliers (must live outside the repo tree; see README)")
	flag.Parse()

	if *asJSON {
		*format = "json"
	}
	if err := run(*base, *repo, *format, *concurrency, *timeout, *useLLM, *intakePath); err != nil {
		fmt.Fprintln(os.Stderr, "correctful:", err)
		os.Exit(2)
	}
}

func run(base, repo, format string, concurrency int, timeout time.Duration, useLLM bool, intakePath string) error {
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

	// Load the policy BEFORE any probe runs: a malformed policy fails loudly
	// here (a broken floor must never fail open), and a missing file simply
	// means no policy.
	pol, err := policy.Load(root)
	if err != nil {
		return err
	}
	// Intake config loads with the policy: authority grants fail loudly
	// before any probe runs.
	var intakeCfg *intake.Config
	if intakePath != "" {
		intakeCfg, err = intake.LoadConfig(intakePath, root)
		if err != nil {
			return err
		}
	}

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

	// Admit external evidence AFTER the in-tree probes: supplied rows join
	// each claim's evidence list and are weighed by the same rules.
	var intakeRecords []schema.IntakeRecord
	if intakeCfg != nil {
		subj := intake.Subject{HeadSHA: change.HeadSHA, InputDigest: change.InputDigest}
		extra, records, err := intake.Run(intakeCfg, root, subj, claims)
		if err != nil {
			return err
		}
		for i := range claims {
			if rows := extra[claims[i].ID]; len(rows) > 0 {
				evidence[i] = append(evidence[i], rows...)
			}
		}
		intakeRecords = records
	}

	r := receipt.Assemble(change, claims, evidence, coverage)
	r.Intake = intakeRecords
	if pol != nil {
		r.Policy = policy.Evaluate(pol, r)
	}

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

	if r.GateBlocked() {
		os.Exit(1)
	}
	return nil
}
