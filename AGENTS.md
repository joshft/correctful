# AGENTS.md

Instructions for AI agents (and humans) working in this repository. This file
is the source of truth; `CLAUDE.md` merely points here.

## What this project is

correctful is a diff-level evidence checker: it reads a change, harvests the
claims the change makes, dispatches mechanical probes to test them, and emits a
receipt with an honest unverified remainder. Read `README.md` for the model.
Four rules are load-bearing (README "The model"): tier is a property of the
probe; the remainder is always stated; refutation dominates; the receipt
discloses its own blind spots.

## Disclosure policy — read before every commit, PR, or issue

This repository is public. Nothing that identifies or describes the
maintainer's host system, local environment, private infrastructure, or private
projects may appear in ANY published artifact: commits (messages AND file
contents AND history), pull requests, issues, comments, test fixtures, or docs.

Concretely, never include:

- absolute or user-local filesystem paths (`/home/...`, `~/...`), hostnames,
  machine names, OS/user account details;
- credentials of any kind — tokens, keys, passwords, signing material — or
  references to where they live;
- internal infrastructure: servers, IPs, ports, cloud accounts, private URLs;
- the NAMES of private projects or repositories, their source code, their
  internal file paths, class/assert/identifier names, or design details.
  Referring to dogfood targets generically ("a real 101-file production
  change", "a real Alloy lifecycle model") is the standard practice.

**Test fixtures are real-shaped, never real.** Parsers here are tested against
fixtures that preserve the exact STRUCTURE of real-world artifacts (naming
conventions, comment styles, attribute placement, output formats) with all
domain identifiers anonymized. Preserving the structure is what makes the test
honest; anonymizing the identifiers is what makes it publishable. When you
capture real tool output (test-runner summaries, model-checker receipts) as a
fixture, scrub project names, paths, and domain vocabulary before committing —
the parsed SHAPE (counts, markers, key layout) is the part under test.

Before pushing: `git log -p` over the outgoing range must satisfy this policy,
not just the tip of the tree. If something slipped into history, rewrite the
unpushed history rather than stacking a "remove" commit on top — a removal
commit publishes the secret it removes.

## Working conventions

- Go, stdlib only. `gofmt`, `go vet`, and `go test ./...` must be clean before
  any commit.
- Commit messages: imperative mood, capitalized, no conventional-commits
  prefix; explain WHY when non-obvious.
- Every extractor/harvester change is gated on measurement: it must raise
  verification on a real change WITHOUT introducing a false bind. A remainder
  that shrinks by lying is not an improvement — when in doubt, leave the claim
  in the remainder.
- A probe runner must never take its verdict from an exit status a tool is
  known to mis-report; parse the tool's own result artifact (summary counts, a
  JSON receipt) and treat "could not run" as distinct from "refuted".
- The schema (`schema/`) is the payload; changes to it are versioned
  (`SchemaVersion`) and deliberate.

## Language for reports and replies

Write all reports, replies, and responses in ASD-STE100 Simplified Technical
English. This section obeys its own rule.

- Use the active voice.
- Write short sentences. Use a maximum of 20 words in an instruction. Use a
  maximum of 25 words in a description.
- Give only one instruction in each sentence.
- Use a word only with one meaning. Do not use a different word for the same
  thing.
- Use the simple present, simple past, or simple future tense. Do not use
  complex tenses.
- Do not use idioms, metaphors, or slang.
- Use the project terms (receipt, claim, probe, remainder, tier, harvest) as
  technical names.
- Put important warnings first.

This rule applies to reports, replies, and responses only. Commit messages,
pull-request bodies, code comments, and documentation keep the conventions
above.

## How changes land

correctful is dogfooded on itself: every change lands through a pull request,
and the CI workflow (`.github/workflows/correctful.yml`) emits a receipt for
the PR's diff as a self-updating comment. That receipt is the merge gate.

- Never commit directly to `master`; branch, push, open a PR.
- Read the receipt before merging. A refuted claim blocks the merge (the
  workflow exits non-zero) and must be resolved by fixing the change — never
  by weakening the probe or rewording the claim out of harvest range.
- The remainder does not block, but it is part of the review: if the receipt's
  remainder misstates what the change leaves unchecked — a false entry, a
  missed claim, a misleading tier — that is a correctful bug the PR just
  surfaced, and fixing it is the point of dogfooding. File or fix it before
  merging.
- Squash-merge, so one PR is one commit on `master`; the PR description
  carries what the receipt could not check and why that is acceptable.
