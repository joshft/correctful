# correctful — design, rationale, and measurements

This is the long-form companion to the [README](README.md): the model and its
load-bearing rules, what each harvester and probe does and why, the design
decisions dogfooding forced, the measured numbers behind every increment, and
the known limitations — stated as prominently as the wins. Nothing here is
needed to *use* correctful; all of it explains why the receipt can be trusted.

Lineage: correctful is the checker the author's prior tools were reaching
for — [`correctless`](https://github.com/joshft/correctless) manufactures
process pressure; an earlier consensus-based reviewer failed for want of a
mechanical oracle; a proof-directed worker proves against a formal spec.
correctful is the layer that turns any of those into evidence on a receipt —
and states, out loud, where the evidence runs out.

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

## The receipt as a PR comment

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
- **The scope boundary states its own blind spots, and the input is pinned.**
  The change resolver deliberately excludes two classes of untracked file —
  never-tracked top-level trees (an installed tool's cache; measured, one
  such tree drowned a 184-file change under 2,000+ files) and hidden paths.
  Those files never reach the harvest, so the coverage section cannot account
  for them; the receipt therefore discloses the exclusions at the scope
  boundary itself (`excluded`: reason, count, and the trees affected — a
  brand-new top-level directory of real work stays invisible until its first
  `git add`, and the receipt now *says so* on every affected run). Beside the
  commit SHAs, `input_digest` pins a SHA-256 over the exact harvested content
  (sorted path + per-file content hash, deletions marked absent), so a
  mid-branch receipt over staged, unstaged, or untracked work — which no
  commit SHA identifies — is reproducible and comparable too.
- **Probe details are machine-clean by a categorical rule, not a blocklist.**
  A receipt is shareable, and runners echo arbitrary tool output (a failing
  test's own message, a compiler error) into evidence details. Every detail
  passes one sanitization chokepoint: the repository root becomes `.` (in-repo
  paths stay fully readable), every OTHER absolute path collapses to
  `…/<basename>` — closing temp paths, toolchain roots, other users' homes,
  and sibling project names in one rule instead of enumerating known-bad
  roots — and the machine's hostname is scrubbed. File:line actionability
  survives the collapse (`…/testing.go:1576`); URLs pass untouched.
- **Same-id claims merge; accept/reject pairs earn T2.** Every test named for
  one invariant becomes a probe of the same claim — all run, any can refute.
  When one of those tests is accept-polarity and another is reject-polarity
  (exact-match name vocabulary, same package), the two collapse into a compound
  pair probe: one `go test -json` invocation under the same event-stream
  discipline as the single runner — the pair passes only on an explicit pass
  event for *each* name (a skipped or renamed-away side exits 0 and must not
  confer T2), a failing side refutes, and an infra failure is not-run, never a
  refutation — conferring T2. The polarity vocabulary is
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

The cardinal rule is structural here, not aspirational: **nothing the model
says can, by itself, raise a tier or produce a false pass.** A proposal is
minted probe-less, lands in the remainder at T0, and is marked
`[llm-proposed]` in every rendering — verification included, so the
provenance is never laundered away. The worst a hallucinated claim can do is
waste a remainder row. What the extractor buys is an honest remainder for
changes that never stated their claims: "this change implicitly asserts X,
Y, Z — none of it checked."

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

### Model-proposed edges (schema 0.0.8): proof-carrying binding for LLM claims

Verifying LLM proposals is where the value loop closes — and where the trust
risk concentrates: a wrong claim→test association plus a passing test would
mint a false verification, the one failure mode the product exists to
prevent. So the model is allowed to propose an EDGE, never to certify one.
A proposal may name, in an optional `test` field, a changed Go test that
directly checks the claim, and the edge must then survive two mechanical
gates:

1. **Existence, at mint time.** The named test's definition must appear in
   the diff the model was shown — an added or context line of a changed
   `_test.go` file (a deleted definition does not count: the test no longer
   exists to run). Exactly one changed test file may define it (an ambiguous
   name fails closed), and the claim's own file must be shipped Go code —
   the target the second gate will check. A failed edge mints the claim
   probe-less, exactly as before; the claim is never rejected for a bad
   edge.
2. **Execution, at run time.** The bound probe's run is instrumented for
   coverage, and the pass counts for the LLM claim ONLY when the profile
   shows execution reaching the claim's file (`binding: file-covered` — the
   file-level analogue of the function-level coverage-proven binding that
   annotated spec-id claims get; an LLM claim carries a file, never a line).
   Fail-closed in both directions: a profile whose execution never touched
   the file REFUTES the edge (`file-not-reached` — which says nothing about
   the claim itself), and a pass with no profile at all raises nothing. The
   remainder row discloses which gate discarded the pass.

Refutation keeps its unconditional dominance: a failing test in the change
blocks the gate no matter whose edge bound it — a mis-attributed refutation
is fail-safe, because the failure itself is real. What the model's word can
now reach, end to end: a remainder row becomes a T1 verified row exactly
when a machine confirmed both that the named test exists in the change and
that its execution demonstrably touches the code the claim is about — still
not proof the test asserts the right property, and the receipt's
`[llm-proposed]` marker plus `[binding: file-coverage-proven]` state
precisely that trust boundary on the row itself.

Measured (first live runs, 2026-08 — three runs over two real merged
diffs): 51 proposals total, 40 bound to a named changed test and verified
with a coverage-confirmed edge (18/19 and 15/16 on the binding feature's
own diff; 7/16 on the pair-runner diff, whose seven bound claims are
exactly its load-bearing behavioral statements — skip-never-confers,
build-failure-is-not-refutation, exact-name matching). All 11 unbound
proposals stayed probe-less for the DESIGNED reasons: claims attributed to
test files or non-Go files, claims of absence, or no test named — and the
one unbindable claim on the feature diff was the same semantic claim in
both runs (the schema version string, which no changed test asserts). Zero
edge rejections fired live — the model proposed no wrong edge in these
runs, so `file-not-reached` remains exercised only by tests — zero
unconfirmed passes, zero refutations, zero hallucinated rows. Honest
caveat: in a tightly-coupled repo, file-level granularity is permissive — a
named test that executes the claim's file while asserting a different
property would still confirm, which is why the verified row states
file-coverage and nothing stronger.

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

Measured (round 2, 2026-08 — recall and repeatability, not just precision):
against BLINDED claim inventories written before any run, on two real merged
changes. Precision held: 115 of 115 proposals across six runs were accurate,
zero hallucinated files or mechanisms. Recall — the number round 1 never
measured — was 11/12 and 9/11: every core behavioral claim surfaced, and the
misses concentrate where a reader would predict — a subtle negative (a code
path deliberately NOT instrumented), a deliberate design omission (counts
disclosed instead of path lists), a documentation-meta claim. Repeatability:
across four runs on identical input, exact-text stability was ZERO — the
model rewords every proposal every run, so the content-hashed ids never
collide — while semantic stability was effectively complete (all four runs
covered the same claim set; counts varied 18–20). Consequence, stated
honestly: `[llm-proposed]` remainder rows are stable in MEANING but not in
id or wording across runs; diffing two receipts' LLM rows textually will
show churn that is not change. One targeted scope fix also shipped measured:
`.github` is project-owned behavior (this repository's merge gate lives
there), so its diff sections now reach the model — ordered AFTER every
non-hidden section so they can never crowd shipped code out of the byte cap.
Live A/B on a real workflow-touching change: pre-fix the workflow claim was
structurally unreachable; post-fix the model minted it from the workflow
section itself, exact to the step name and setting.

## The end state (adopted 2026-08, refined by external review)

The definition the project builds toward:

> A receipt is an authenticated record of claims, evidence, scope, and
> unverified remainder for one exact change.

And the operating principle it exists to enable:

> correctful makes each change carry its evidence and its unverified
> remainder. Human review effort then follows unresolved claims, not agent
> output.

The context is agentic engineering: generation is no longer the bottleneck,
verification is. An agent can produce fifty changes a day; a human cannot
read fifty diffs, but a human can read fifty remainders. The same boundary
serves orchestration — an agent must never accept another agent's report
about its own success, only a receipt from a trusted execution context.

The product structure this implies:

- Agents and humans propose claims.
- Trusted harvesters find additional claims.
- Probe suppliers produce mechanical evidence.
- correctful validates bindings and verdicts.
- Repository policy evaluates the receipt.
- Agents use receipts as work contracts.
- Humans review refutations, remainders, and exceptions.
- Receipts link across changes through stable identities and digests.

The boundary: **correctful validates, binds, combines, and applies policy.
It does not become every probe runner.** The shared schema is worth more
than a large built-in probe set — external tools supply evidence through
the schema (with provenance, once the evidence-intake contract exists), and
the in-tree runners are the demonstration, not the product.

Five commitments, each a correction the external review made to a weaker
draft of this section:

1. **The receipt decides when diff review is necessary; it never fully
   replaces it.** High-risk remainders, weak bindings, and unexpected
   claims trigger source review. Verified rows need little routine
   attention — but policy, harvester behavior, and probe definitions are
   the trust base, and changes touching them always warrant human eyes.
2. **Tier floors need evidence classes.** A single scalar tier cannot
   express relevance, binding precision, execution scope, or environment.
   Policy requires a minimum tier AND a probe type ("authentication code
   requires a T2 adversarial pair and an integration probe").
3. **An input digest alone is not a chain.** A receipt chain also needs the
   parent receipt digest, base and head identities, the policy digest,
   harvester and probe versions, and stable repository claim identities.
   Known open problem: LLM claim ids are content hashes and churn across
   runs (measured: zero exact-text repeatability), so chain comparability
   currently holds for mechanical claims only.
4. **The trusted system creates the receipt.** A subagent must not create
   the receipt that evaluates its own work. Today this holds by
   construction in the PR gate (CI creates the receipt on the merge ref;
   local receipts are advisory); the missing leg is authentication — a
   signature binding the receipt to the runner and the exact input digest.
5. **Receipts carry evidence, not proof.** Formal systems supply proof;
   tests, fuzzers, and observations supply weaker but real evidence. The
   tier ladder preserves this difference and the receipt never states more
   than the probe demonstrated.

## The policy layer (schema 0.0.11): evidence floors per path

The end state's second commitment, delivered: `correctful.json` at the repo
root declares rules — paths plus a floor (`min_tier`, optionally a required
`mechanism` and a required `scope`). Evaluation is per changed file, and
the tie between a file and its evidence is STRUCTURAL, never assumed: a
verified claim speaks for a file only when the claim was sourced from it
(LLM claims with confirmed edges, spec-ids in code) or a reference site in
shipped code names it. A matched file nothing demonstrably ties evidence
to is a miss stated exactly that way — which makes floors honest and also
scopes where they are USEFUL: repos that annotate code with claim ids, or
run the LLM extractor. A floor on unannotated code fails, and should.

Design decisions worth recording:

- **Misses block the gate**, same as refutations — a floor that only
  informs is not a floor. The exit-gate line in the receipt says which legs
  block.
- **Test files are exempt and the exemption is counted** — a `_test.go` is
  evidence, not an evidence subject. Silent exemption would be a coverage
  lie; the receipt shows the count.
- **The policy digest is the second chain field** (after the tool
  version): SHA-256 over the policy file's exact bytes, rendered short
  beside the change. A policy change — the trust base changing — is
  visible in the receipt chain, which is the review trigger the end
  state's first commitment asks for.
- **A malformed policy fails loudly before any probe runs.** A broken
  floor must never fail open; a missing file simply means no policy.
- **The LLM edge gate applies identically here** — `Evidence.CountsFor`
  moved to the schema so weighing and policy evaluation share one
  definition and can never disagree. A pass on an unconfirmed
  model-proposed edge satisfies no floor.
- **Each rule stands alone**: a file matched by two rules must satisfy
  both floors; the miss row names the violated rule and the best tied
  evidence, so the reader sees the gap, not just the verdict.

Measured (first live run, 2026-08, on a real annotated repo's feature
branch): a T1 floor over the changed command directory matched 26 files —
8 exempt as test files (disclosed), 18 evaluated, ALL 18 missed with "no
verified claim ties to this file", and the gate blocked. Correctly: the
branch's 153 verified claims are all test-name-sourced, and none of the
changed code files carries a reconciled claim id, so no verified evidence
structurally speaks for them. The strictness is the finding — a floor
demands the tie discipline (id-annotated code reconciled with id-named
tests, or LLM extraction with confirmed edges), and states exactly what is
missing when a repo has not adopted it.

## The evidence-intake contract (schema 0.0.12): external suppliers

The probe-supplier surface, opened: an external tool submits evidence rows
through a schema-shaped intake document, and correctful validates, binds,
combines, and applies policy — without becoming every probe runner. The
first draft of this contract was reshaped by an adversarial pre-implementation
design review; the five critical findings it surfaced are now the design:

1. **The row must not control its own authority.** The draft let a
   document carry its own tier and mechanism — one JSON file could mint T4
   evidence for any claim. Authority now lives in an invoker-owned
   PROFILE: the intake config fixes each supplier's name, mechanism, and
   maximum tier (the external analogue of `Runner.MaxTier`), and a row
   reports an OUTCOME only.
2. **Outcomes are an enum, not booleans.** `verified / counterexample /
   inconclusive / not_run / error` — only a counterexample refutes. A
   proof failure can mean *unproved*; a fuzzer timeout means *incomplete*;
   neither is a refutation. This is the same lesson the go-test runners
   learned from `t.Skip` exiting 0, applied at the contract boundary.
3. **Subject identity is head SHA plus input digest, both required.** A
   mismatch rejects the whole document as stale, disclosed. Known
   boundary, stated: this names the committed tree and the changed-file
   overlay, not the dependency closure — a full source-snapshot digest is
   a later field.
4. **Selective reporting is mitigated, not solved.** A profile can be
   `required`: a required supplier with no admitted document blocks the
   gate. Rejected rows are listed with identity and reason, never reduced
   to a count — a rejected counterexample naming an unknown claim can
   expose claim drift and must stay visible. The full manifest protocol
   (correctful hands the supplier a probe manifest; the supplier returns
   one outcome per entry) is deferred and named.
5. **A claim-id match is not a binding.** Rows for ambiguously anchored
   claims are rejected; every admitted row carries `binding:
   supplier-attested` — the receipt's statement that the tie is the
   supplier's word. Claim fingerprints are deferred.

Mechanics that follow the same discipline: probe ids are NAMESPACED BY
CONSTRUCTION (`ext:<supplier>/…` — a blacklist of built-in prefixes would
be enumeration, and enumeration is how extractors go class-incomplete);
strict JSON decoding rejects unknown fields; documents and config must be
regular files OUTSIDE the repository tree, symlinks rejected — evidence
the reviewed change can write is not evidence; every external string is
control-stripped and bounded, and details pass the same sanitization
chokepoint as in-tree evidence; the receipt's intake records carry the
admitted document's SHA-256 as the audit pin. Policy floors may reference
supplier mechanisms, so the policy mechanism vocabulary opened from an
enum to a token shape — a typo now fails closed as an unsatisfiable floor
instead of loudly at load, and the miss row shows the gap.

Residual trust, stated plainly: admission authenticates possession of the
invoker's config, not origin, and a trusted supplier can still lie, omit
work, or bind the wrong property. The `[external: … — supplier-attested]`
marker on every acting row is the receipt refusing to launder that trust
into the appearance of an in-tree run. The signature channel (binding a
document to a runner identity) is the planned stronger leg.

Measured (first live runs, on this repository's own change): an admitted
document verified a real claim at T4 with the external marker, disclosed
an unbound counterexample among its rejections, and left the gate green;
flipping the row to a counterexample refuted the claim, marked the
refutation external, and blocked; deleting the required document blocked
with "not admitted — REQUIRED" on the intake line. All three gate legs
behaved to specification on the first run.

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
- **"Unread" had merged two causes — RESOLVED (schema 0.0.7).** A file can be
  unread because no harvester understands its format (a capability gap) or
  because policy excludes it (hidden-directory tooling). The two are now
  separate disclosures: per-file `skip_reason` ("no-harvester" vs
  "hidden-path"), an `unread_policy` summary count, and two distinct
  histogram lines in every renderer. Found live on the first pre-push
  dogfood install: a repo's tracked hidden documents rendered as "no
  harvester for .md" when a markdown harvester exists — the cause was
  policy, and the receipt now says so.
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
- **MUST clauses have no probe-carrying path — measured, not neglected.**
  Binding a clause to a test needs a join signal a test can mechanically
  name. Measured across every qualifying corpus: the one real RFC's 58
  keyword clauses contain zero spec identifiers and zero requirement labels,
  only 5 carry a backticked code token, and that repository's tests are
  shell/C infrastructure suites correctful has no runner for. A fuzzy
  textual join would mint false bindings — the same class the doc-comment
  binding was cut for — so clauses stay honestly in the remainder. The
  bindable form is explicit: a document that labels its requirements
  (`R-007`-style) with tests naming the label gets binding through the
  existing test-name reconciliation; support lands when a corpus adopts
  labels.

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
