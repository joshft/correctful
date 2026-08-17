# correctful

**A diff-level evidence checker.** correctful reads a change, derives the
**claims** it makes, dispatches mechanical **probes** to test those claims, and
emits a **receipt** — including the part every other tool hides: the honest
**unverified remainder**, the explicit accounting of what nothing checked.

> Is this diff *correctful*?

correctful is open source (MIT, open schema). It is the checker the author's
prior tools were reaching for: [`correctless`](https://github.com/joshft/correctless)
manufactures process pressure; an earlier consensus-based reviewer failed for
want of a mechanical oracle; a proof-directed worker proves against a formal
spec. correctful is the layer that turns any of those into **evidence on a
receipt** — and states, out loud, where the evidence runs out.

## The model

```
change ──▶ harvest claims ──▶ dispatch probes ──▶ weigh evidence ──▶ receipt
                                                                      ├─ verified   (a probe ran and passed)
                                                                      ├─ refuted    (a probe ran and failed → gate blocks)
                                                                      └─ remainder  (nothing checked it — stated, not hidden)
```

Four rules are load-bearing:

1. **A claim's evidence tier is a property of the probe, never an opinion.** An
   LLM may *propose* claims; only a machine may raise a claim above T0. The
   failure modes are benign by construction: a hallucinated claim wastes a probe;
   a missed claim degrades to the status quo.

2. **The remainder is always stated**, even when empty. Its absence is a declared
   result, not an omission.

3. **A refutation by any probe dominates.** A claim bound by five tests where
   four pass and one fails does not hold — passing probes never outvote a
   failing one.

4. **The receipt discloses its own blind spots.** Every receipt carries a
   coverage section splitting the change's files into *claimed* (sourced a
   claim), *scanned* (read, nothing claimed), and *unread* (no harvester opened
   it) — with the unread files grouped by extension. A receipt that verifies
   17 of 17 claims while 266 of 414 files went unread says so in the same
   breath; this is the remainder philosophy applied to the tool itself.
   Harvesters report what they read from inside their own read path, never from
   a parallel predicate that could drift from the real filter.

### Evidence tiers

| Tier | Meaning | Example probe |
|------|---------|---------------|
| T0 | unverified — the remainder | (none bound, or did not run) |
| T1 | a single assertion held | one `go test` |
| T2 | an accept/reject pair held | positive + negative test pair |
| T3 | property / differential / model-check | property test, cross-impl parity, Alloy/TLA+ |
| T4 | proof / exhaustive / observed | Dafny proof, exhaustive enum, eBPF-observed |

## Quickstart

```sh
go build -o correctful ./cmd/correctful

# per-change receipt: everything this branch adds over main
./correctful -repo /path/to/repo -base main

# whole-tree sweep (the accumulated-state organ)
./correctful -repo /path/to/repo

# the payload, for other tools to read
./correctful -repo /path/to/repo -base main -json
```

Exit status is `0` when nothing was refuted and `1` when a probe ran and a claim
did not hold (merge-gate semantics). **The remainder never fails the run** — it
is an honest report, not a defect.

## As a merge gate

```sh
# in pull-request CI: detect the base, emit a PR comment, gate on the exit code
correctful -base auto -format md > receipt.md
```

`-base auto` resolves `$GITHUB_BASE_REF` (pull-request CI), then the remote's
default branch, then local `main`/`master` — and the chosen ref is printed in
the receipt's change header, so the resolution is never silent. The markdown
receipt starts with a `<!-- correctful-receipt -->` marker so a workflow can
find and update its previous comment instead of stacking a new one per push;
`.github/workflows/correctful.yml` in this repo is the reference wiring.

The comment's ordering is the product's opinion: refutations first and loud,
the **unverified remainder always visible and never collapsed**, the verified
list folded away, and coverage closing the comment so "all verified" can never
be read apart from "out of how much". The gate blocks on refuted claims only;
the remainder and coverage inform the reviewer, they do not fail the build.

## v0 scope: the harvest path

v0 ships the **harvest** extractor — the one with zero extraction risk. It reads
claims that are *already written* into the change:

- **Go test names → claims with a bound probe.** A test named for an invariant —
  at the start (`TestINV009_…`) or after a cluster prefix (`TestClusterC_INV004_…`,
  `TestClusterB_INV007a_…`) — becomes an invariant claim whose probe is that test;
  a pass confers T1. Detection is segment-based (split the name, match whole
  segments), so it reads ids embedded mid-name that a leading word-boundary scan
  misses, without the false positives a substring scan would admit. Parsed with
  `go/ast`, not text-scraped.
- **Spec identifiers in code → invariant claims.** `INV-…/BND-…/PRH-…/ABS-…/
  PAT-…/TB-…/AP-…` named in shipped code. If a same-id test was harvested, the
  two reconcile into one verified claim; otherwise the identifier lands in the
  remainder — an invariant the change *names* but no test binds.
- **C# xUnit tests → claims with a bound probe.** In xUnit codebases following
  the methodology the invariant id lives in the CLASS name — mixed case, often
  several ids, sometimes with a continuation (`Inv018AcceptancePolicyTests`,
  `Inv010Inv011ParityHarnessTests`, `Inv009And010LoaderTests` → INV-009 and
  INV-010). Every `[Fact]`/`[Theory]` method of an id-carrying class becomes a
  probe of that class's invariant(s); merging unions them, so one invariant
  claim carries a probe per method — across files. Probes run as
  `dotnet test --filter FullyQualifiedName~Class.Method`, and the verdict comes
  from the summary COUNTS, never the exit status: measured empirically, a filter
  matching no test still exits 0, so trusting the exit code would mint a pass
  for a phantom test. A probe id bound under several claims executes once; its
  single verdict is attributed to every claim that binds it.
- **Alloy models → safety-assert and witness claims; checks earn T3.** Every
  `assert Name { … }` in a changed `.als` file becomes a safety-assert claim,
  bound to a probe iff the file also declares `check Name` — an assert with no
  check lands in the remainder, the formal-methods remainder in its purest form.
  Every `run Name` becomes a witness claim with INVERTED pass semantics: the
  model must admit the scenario (the vacuity guard — a fleet of passing safety
  checks means nothing if the model is inconsistent). The runner executes the
  whole file's command set in one Alloy CLI invocation (`exec --command '*'
  --type json`, jar resolved from `$ALLOY_JAR` or `~/.cache/correctful/`, never
  downloaded mid-receipt) and shares the parsed JSON receipt across all of the
  file's probes; a counterexample refutes concretely. Bounded model checking
  confers T3 — stronger than an assertion, short of proof.
- **Same-id claims merge; accept/reject pairs earn T2.** Every test named for
  one invariant becomes a probe of the same claim — all run, any can refute.
  When one of those tests is accept-polarity and another is reject-polarity
  (exact-match name vocabulary, same package), the two collapse into a compound
  pair probe: one `go test` invocation, verified from verbose output that *both*
  actually executed and passed, conferring T2. The polarity vocabulary is
  deliberately conservative — a mis-paired probe would mint an unearned T2, and
  ambiguous names classify as neither polarity.

Later increments (each gated on beating the one before it): structural extractor
(v0.5), LLM extractor (v0.7), and purpose-built probes/harvesters for Alloy
asserts, RFC MUST clauses, mutation, fuzzing, concolic execution, Dafny proof,
and runtime observation.

## Known limitations (found by dogfooding, stated honestly)

correctful was run on itself and on a real 101-file production change on its
second day. These limitations surfaced immediately and are recorded here rather
than smoothed over:

- **Coverage is name-attested, not proven.** A test binds an invariant because
  its *name* says so; correctful trusts that annotation and does not verify the
  test's assertions actually exercise the invariant. This is the same trust every
  test-naming convention already asks of a reader — but it is trust, not proof.
  A stronger binding (does the test reach the code the invariant governs?) is a
  later, proof-carrying increment.
- **Bare ids don't reconcile with sub-variant tests.** A source reference to
  `INV-007` is not matched by tests named for `INV-007a…e`; correctful leaves
  bare `INV-007` in the remainder rather than assume the parts cover the whole.
  Conservative by design — silence over a false pass.
- **Doc-comment binding was tried and cut.** An earlier v0.5 also bound ids named
  in a test's doc comment. Measured against a real production change, it produced false passes —
  it bound antipatterns and invariants that were merely *cross-referenced*
  ("unlike AP-004, …"), not covered. A remainder that shrinks by lying is not an
  improvement, so only the high-precision name signal survives. The measurement
  is the point: every extractor increment is kept only if it raises verification
  *without* a false bind.
- **C# harvesting is a comment-aware line scanner, not a parser.** No
  stdlib-grade C# parser is available to a pure-Go binary; a tree-sitter sidecar
  is a later increment. The scanner's failure direction is benign by
  construction: a phantom method mints a probe whose filter matches nothing,
  which the runner records as `Ran=false` — never as a pass. xUnit only for now.
- **Spec identifiers are not namespaced per project.** `AP-012` in one repo and
  `AP-012` in another are different invariants that share an id; receipts are
  per-repo today, so this only matters for a future cross-repo ledger.
- **"Unread" merges two causes.** A file can be unread because no harvester
  understands its format (a capability gap) or because policy excludes it
  (hidden-directory tooling). The per-file JSON makes the cause inspectable;
  the text histogram does not yet distinguish them.
- **Spec-id harvesting skips hidden directories.** Installed tooling under
  dot-directories (`.correctless/`, `.claude/`) carries the tooling's own
  identifiers; measured on a real sweep, all 75 remainder entries were tooling
  ids and zero were project code. Tooling makes its own claims — not the
  repo's.
- **Spec-id harvesting is restricted to code files** (see `codeExts`). Scanning
  prose catalogs (an architecture doc, an index) reproduced the
  extraction-over-prose class — identifiers *listed* as documentation are not
  claims the change *makes*. Structured non-code sources (Alloy, RFC MUSTs) are
  the domain of their own purpose-built harvesters, not the token scanner.

## Layout

```
schema/                 the payload — Claim, Probe, Evidence, Tier, Receipt
internal/gitdiff/       resolve the change (diff vs base, or whole tree)
internal/harvest/       diff → claims (go-test names, spec-id references)
internal/probe/         claims → evidence (dispatcher + go-test runner)
internal/receipt/       assemble + render (JSON payload, text for humans)
cmd/correctful/         the CLI
```

## License

MIT. The schema is the payload; the tool is its demo.
