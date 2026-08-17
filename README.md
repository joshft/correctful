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

v0 ships the **harvest** extractor — the lowest-risk one: it cannot invent a
claim, only read claims somebody wrote. That does not make its remainder free —
what it reads can still be noise (fixture identifiers, catalog mentions), so
remainder precision is measured the way binding precision is, and the noise
classes dogfooding found (test-file fixtures, mention-only "RFC 2119"
qualification) are cut, not tolerated. It reads
claims that are *already written* into the change:

- **Go test names → claims with a bound probe.** A test named for an invariant —
  at the start (`TestINV009_…`) or after a cluster prefix (`TestClusterC_INV004_…`,
  `TestClusterB_INV007a_…`) — becomes an invariant claim whose probe is that test;
  a pass confers T1. The verdict comes from the `go test -json` EVENT stream,
  never the exit status: measured empirically, a test that calls `t.Skip`
  exits 0, so exit-code trust would confer T1 on a test that asserted
  nothing. Only an explicit `pass` event for the exact test verifies; only an
  explicit `fail` event refutes; skip, no-match, build failure, and
  cancellation are all Ran=false — they verify nothing and refute nothing.
  Detection is segment-based (split the name, match whole
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
- **RFC MUST clauses → probe-less claims: the honest T0 tenant.** A document
  that identifies as normative — an `rfc` filename segment, a first heading
  beginning `# RFC`, or RFC 2119 / BCP 14 boilerplate — is harvested for its
  UPPERCASE keyword clauses (`MUST`, `MUST NOT`, `SHALL`, `SHALL NOT`,
  `REQUIRED`): one claim per sentence, wrapped lines unwrapped, blockquote
  markers stripped, ```text fences included (measured: a real RFC keeps its
  most load-bearing formula in one), code-labeled fences and lowercase "must"
  prose excluded, the 2119 boilerplate itself never a claim. Every clause
  lands in the remainder — no name trick binds an RFC sentence to a test.
  That is the point: the receipt now *states* the normative surface nothing
  checks instead of silently not knowing it exists. `SHOULD`/`MAY` are
  excluded — the remainder carries obligations, not options.
- **Spec-id claims anchor to their definitions — or the receipt says why not.**
  The first rung of proof-carrying binding. Every id-shaped claim resolves
  against the repo's definition corpus (tracked markdown, dot-dirs INCLUDED —
  claims may only *originate* from shipped code, but they *resolve* against
  documents anywhere): a definition is a heading of the form `### INV-004:
  <title>`, with an explicit separator required so a fixture file merely
  *named* for an id defines nothing. A uniquely-titled id **resolves** and the
  claim adopts the definition's own words; an id defined differently in
  several specs is **ambiguous** — id namespaces are feature-local in practice
  (a measured corpus had 14 of 34 spec files each defining their own INV-004)
  — unless exactly one definition lies inside the changed files themselves
  (the change carrying its own spec has declared its namespace: a mechanical
  join, not a guess); an id the corpus never defines is an **orphan**, named
  in the receipt. Sub-variant ids (`INV-013d`) resolve through their parent
  without adopting its title. Measured on a real 101-file change: 53 of 72
  spec-id claims resolved, 17 honestly ambiguous, and the 2 orphans were both
  real findings — an antipattern id cited in a PR title but never added to
  the catalog, and an invariant no document defines. A repo that defines **no
  spec-id corpus at all** is not practicing the convention: a probe-less
  reference there has no possible referent, so it is a **mention**, not a
  claim — suppressed rather than minted into the remainder, with the count
  disclosed in the receipt's coverage. The gate sits at the premise level
  deliberately: measured across two real corpora (2,880 and 642 sightings),
  the dominant real-assertion shape is a mid-comment parenthetical id —
  textually identical to an explanatory example — so no stricter annotation
  grammar could separate the two without destroying most of the real harvest.
  Measured on this repo's own sweep: the remainder's 7 example-id rows (0 of
  7 were real invariants) became a single disclosed suppression line, while
  both corpus-bearing dogfood repos produced byte-identical receipts.
- **Go probe bindings are coverage-proven where the code is annotated.** The
  second rung. When a claim's id is also written into shipped code (its
  *reference sites*, preserved through claim merges), the go-test probe run
  is instrumented (`-coverprofile -coverpkg=./...`; measured cost ~0.1s per
  warm run) and each claim↔probe edge is checked against the one shared
  profile: did execution reach the enclosing function of any annotated site?
  **covered** upgrades the trust story from "the test says it is about
  INV-004" to "the test demonstrably executes the code annotated with
  INV-004"; **name-only** says the binding was checked and no annotated
  region was reached — a disclosure, not an accusation (on the measured
  change, both name-only rows were true: one probe was an AST-inspection
  test that reads the annotated function without executing it, the other
  exercised the library beneath the annotated cmd-level enforcement sites).
  Instrumentation never degrades a verdict: an instrumented run that cannot
  execute falls back to a plain run and simply carries no binding statement.
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

## The LLM extractor (v0.7, opt-in)

Everything above harvests claims somebody WROTE — test names, spec ids, Alloy
asserts, MUST clauses. The wild case is a diff where nobody wrote any of that,
and there the harvest path is honest but empty. `-llm` adds the extraction
rung for that case: a language model reads the DIFF (never the repository) and
proposes the claims the change makes implicitly. Be aware what that means
operationally: **the capped diff — your source changes — is transmitted to the
Anthropic API.** Nothing else leaves the machine, and nothing at all does
without the flag.

The cardinal rule is structural here, not aspirational: **proposals carry no
probes**, land in the remainder at T0, and are marked `[llm-proposed]` in
every rendering — nothing the model says can raise a tier or produce a false
pass. The worst a hallucinated claim can do is waste a remainder row. What the
extractor buys is an honest remainder for changes that never stated their
claims: "this change implicitly asserts X, Y, Z — none of it checked."

Discipline: pinned model (`claude-sonnet-5`; `CORRECTFUL_LLM_MODEL`
overrides), a strict JSON output contract that fails loudly on
prose-wrapped output, and proposals rejected — never repaired — when they
name a file outside the diff or a shape outside the taxonomy. The diff is
capped at 150KB with truncation disclosed through coverage: only files whose
hunks were actually sent count as read by the `llm` harvester. Requires
`ANTHROPIC_API_KEY`; without the flag, behavior is byte-identical to the
mechanical path, so the CI gate never depends on a model.

```sh
ANTHROPIC_API_KEY=... correctful -base auto -llm
```

Verifying LLM proposals (binding them to probes) is a later increment, gated
like every extractor before it: kept only if it raises verification without a
false bind.

Measured (first live runs, 2026-08): on a wild-case diff — a real 19-file
change with zero pre-written claims — 19 of 20 proposals were accurate,
concrete, and falsifiable, with zero hallucinated files or mechanisms; the
one miss over-generalized a rule that real code scopes narrowly (a misreading,
not an invention — and exactly the kind of claim a probe could refute). On a
methodology-rich repo the first run exposed a real blind spot: the change's
own dot-directory documentation sorted first in the diff and consumed the
entire byte cap, so the model re-extracted the docs' claims and never saw
shipped code. Hidden-directory sections are now excluded before the cap is
spent — the same rule every mechanical harvester applies — after which all 20
proposals were grounded in shipped code and user-facing docs, including CLI
behavior no id-bound test states, and the most specific one checked exact
against code down to the flag semantics. The parse gate also earned its keep
live: it caught a truncated response (raising the token budget for a
reasoning-model generation) and a deprecated request parameter, both loudly.

## Known limitations (found by dogfooding, stated honestly)

correctful was run on itself and on a real 101-file production change on its
second day. These limitations surfaced immediately and are recorded here rather
than smoothed over:

- **Binding proof has honest edges.** Anchoring checks an id *exists*;
  coverage-proven binding checks the probe *reaches* the annotated code. What
  remains trust: "covered" does not prove the test asserts the right property
  about the region it reaches; the prover sees in-process execution only
  (a test that drives a subprocess would read name-only); it evaluates plain
  go-test probes (pair probes and C#/Alloy probes carry no binding statement
  yet); and an annotation outside any function — on a const block, a file
  header — is not checkable, so the claim carries no statement rather than a
  guess. Where nothing was checked, the receipt says nothing.
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
- **Normative documents must identify themselves.** The RFC harvester harvests
  a document only when it says it is one — an `rfc` filename segment, a
  `# RFC` first heading, or 2119 boilerplate. Measured on a real repo: 1 of
  ~130 candidate documents qualified, with zero false qualifications — but a
  document stating obligations without any of the markers (say, a `guarantees.md`)
  is sniffed and passed over, and its clauses stay out of the remainder. The
  coverage section still lists the file as scanned, so the blind spot is
  disclosed rather than hidden. Sentence extraction is heuristic (terminal
  punctuation + capitalization); the receipt quotes clauses verbatim, so a
  mis-split is visible in the receipt itself.

## Layout

```
schema/                 the payload — Claim, Probe, Evidence, Tier, Receipt
internal/gitdiff/       resolve the change (diff vs base, or whole tree)
internal/harvest/       diff → claims (test names, spec ids, Alloy, RFC MUSTs)
internal/llmextract/    diff → PROPOSED claims (opt-in -llm; remainder-only)
internal/probe/         claims → evidence (dispatcher + go-test runner)
internal/receipt/       assemble + render (JSON payload, text for humans)
cmd/correctful/         the CLI
```

## License

MIT. The schema is the payload; the tool is its demo.
