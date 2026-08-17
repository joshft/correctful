# correctful

> Is this diff *correctful*?

**correctful is a diff-level evidence checker.** It reads a change, derives
the **claims** the change makes, runs mechanical **probes** against them, and
emits a **receipt** — including the part every other tool hides: the
**unverified remainder**, an explicit accounting of what nothing checked.

CI answers "did the tests pass?". A receipt answers the question a reviewer
actually has: **what does this change claim, which claims are backed by
evidence, and which are backed by nothing?**

- **Refuted claims block the merge.** One failing probe outvotes any number of
  passing ones.
- **Verified claims carry an evidence tier** that is a property of the probe,
  never an opinion — an LLM may *propose* claims, only a machine can verify one.
- **The remainder is always stated**, even when empty, and the receipt
  discloses its own blind spots: which files no harvester read, what the
  change scope excluded.

MIT, open schema. The receipt JSON is the payload; the tool is its demo.

## What a receipt looks like

The PR-comment rendering (illustrative content, real shape):

```markdown
## correctful receipt

**9 claims** — ✅ 6 verified · ❌ 1 refuted · ⚠️ 2 unverified

Change: `main...feature/rate-limit` (2f9c01ab34cd..77aa10becc02 · input:9e51c7d2a418) — 6 files

### ❌ Refuted — a probe ran and the claim did not hold

| Claim | What failed |
|---|---|
| `INV-012` | limiter_test.go:88: burst of 51 admitted; want capped at 50 |

### ⚠️ Unverified remainder (2) — what nothing checked

| Claim | Statement | Source |
|---|---|---|
| `INV-007` | INV-007 (referenced; no bound probe from harvest) | `internal/limit/window.go:41` |
| `MUST:docs/rfc.md:3` | Rejected requests MUST carry a Retry-After header. | `docs/rfc.md:57` |

<details><summary>✅ Verified (6)</summary> … </details>

**Harvest coverage:** 6 files — 4 claimed · 1 scanned · 1 unread
```

## Install

Needs Go 1.26+ and git. Probes use the toolchains you already have — `go test`
always; optionally the `dotnet` CLI (C# probes) and Java + the Alloy jar
(`ALLOY_JAR`) when your repo has those.

```sh
go install github.com/joshft/correctful/cmd/correctful@latest
# or, from a clone:
go build -o correctful ./cmd/correctful
```

## Run

```sh
correctful -base main               # per-change receipt: what your branch adds over main
correctful                          # whole-tree sweep: every claim in the repo
correctful -base main -format json  # the machine-readable payload
correctful -base auto -format md    # CI mode: detect the base, render a PR comment
correctful -base main -llm          # opt-in: an LLM PROPOSES extra claims (remainder-only,
                                    # needs ANTHROPIC_API_KEY; the capped diff is sent to the API)
```

Exit `0`: nothing refuted. Exit `1`: a probe ran and a claim did not hold —
merge-gate semantics. **The remainder never fails a run** — it is an honest
report, not a defect.

## Use it as a merge gate

[`.github/workflows/correctful.yml`](.github/workflows/correctful.yml) is the
reference wiring: one execution per pull request, the receipt posted as a
self-updating PR comment (found again by its `<!-- correctful-receipt -->`
marker), a copy in the job summary, and the gate failing only on refuted
claims. This repository develops through that gate — every PR here carries
its own receipt.

## What it checks today

| You write | correctful derives | Probed by | Tier on pass |
|---|---|---|---|
| a Go test named for an invariant (`TestINV009_…`) | an invariant claim bound to that test | `go test -json` event stream — never the exit code | T1 |
| an accept/reject test pair | one compound adversarial claim | one `go test -json` run; both sides must pass | T2 |
| a C# xUnit class named for invariants | a claim per id, a probe per `[Fact]`/`[Theory]` | `dotnet test --filter`, verdict from summary counts | T1 |
| an Alloy `assert` with a `check` | a safety claim | the Alloy CLI's own result artifact | T3 |
| an Alloy `run` | a consistency witness (inverted pass — the vacuity guard) | the Alloy CLI | T3 |
| spec ids in shipped code (`INV-…`, `AP-…`, …) | a referenced-invariant claim, anchored to its spec definition, coverage-proven where possible | reconciles with id-named tests | — |
| MUST clauses in a normative doc | a must-clause claim | nothing yet — the honest remainder | T0 |
| nothing at all (`-llm`) | LLM-*proposed* claims, marked `[llm-proposed]` | nothing, structurally — a model cannot raise a tier | T0 |

Evidence tiers: **T0** unverified · **T1** one assertion held · **T2** an
accept/reject pair held · **T3** property / model-check · **T4** proof /
exhaustive / observed.

## Design, measurements, limitations

Every extractor and probe above was measured on real repositories before it
shipped, and the misses are documented as prominently as the hits — including
the LLM extractor's blinded precision/recall numbers and the increments that
were measured and *rejected*. The full story — the four load-bearing rules,
harvester rationale, proof-carrying binding, receipt sanitization, and known
limitations — is in **[DESIGN.md](DESIGN.md)**.

## License

MIT.
