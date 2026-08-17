# correctful

> Is this diff *correctful*?

**correctful is a diff-level evidence checker.** The tool reads a change and
harvests the claims that the change makes. The tool runs mechanical probes to
test each claim. Then the tool writes a **receipt**. The receipt includes the
**unverified remainder**: the list of claims that no probe examined. Other
tools hide this list. correctful shows it.

Your CI tells you that the tests passed. The receipt tells you more:

- The claims that the change makes.
- The claims that have evidence, and the tier of that evidence.
- The claims that have no evidence.

Three rules control the receipt:

- **A refuted claim stops the merge.** One failed probe is stronger than many
  passed probes.
- **Only a machine can verify a claim.** An LLM can propose a claim, and it
  can point to a changed test. The pass counts only when coverage data shows
  that the test runs the claim's file.
- **The receipt always shows the remainder**, also when the remainder is
  empty. The receipt also shows its own blind spots: the files that no
  harvester read, and the files that the scope excluded.

The license is MIT. The schema is open. The receipt JSON is the payload.

## The receipt

This is the pull-request format. The content is an example. The shape is real.

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

## Installation

You need Go 1.26 or later, and git. The probes use the toolchains that you
have:

- `go test` — always available with Go.
- The `dotnet` CLI — necessary only for the C# probes.
- Java and the Alloy jar (`ALLOY_JAR`) — necessary only for the Alloy probes.

```sh
go install github.com/joshft/correctful/cmd/correctful@latest
# or, from a clone:
go build -o correctful ./cmd/correctful
```

## Operation

```sh
correctful -base main               # make a receipt for the changes on your branch
correctful                          # examine the full repository (the sweep)
correctful -base main -format json  # write the receipt as JSON, for other tools
correctful -base auto -format md    # CI mode: find the base, write a PR comment
correctful -base main -llm          # also let an LLM propose claims (see the note)
```

The exit code is `0` when no probe refuted a claim. The exit code is `1` when
a probe refuted a claim. **The remainder does not change the exit code.** The
remainder is a report, not a defect.

Note: The `-llm` option sends the capped diff — your source changes — to the
Anthropic API. The option needs `ANTHROPIC_API_KEY`. Without the option, no
data goes out from your machine. An LLM proposal stays in the remainder,
unless a coverage-checked test in the change verifies it.

## The merge gate

The file [`.github/workflows/correctful.yml`](.github/workflows/correctful.yml)
shows the reference configuration:

- The workflow makes one receipt for each pull request.
- The workflow writes the receipt as a comment on the pull request. The
  marker `<!-- correctful-receipt -->` identifies the comment. The workflow
  updates the same comment after each push.
- The gate fails when a probe refuted a claim, or when the change missed a
  declared policy floor.

This repository uses this gate for each of its own pull requests.

## Policy floors (optional)

You can declare evidence floors for the paths that matter most. Write a
`correctful.json` file in the repository root:

```json
{
  "policy_version": 1,
  "rules": [
    {
      "name": "auth-floor",
      "paths": ["internal/auth/..."],
      "min_tier": 2,
      "mechanism": "go-test-pair"
    }
  ]
}
```

The rule reads: each changed file under `internal/auth/` must have one
verified claim that connects to that file, at tier T2 or higher, from an
accept/reject test pair. A rule can also demand a measured execution scope
(`"scope": "cross-package"`).

- A connection is structural. The claim's source file, or a reference site
  in the code, must name the changed file. The tool does not guess.
- Test files are exempt. They supply evidence; they do not need evidence.
  The receipt shows the count of exempt files.
- A missed floor blocks the gate, in the same way as a refuted claim. The
  receipt shows each miss with the best found evidence and the floor.
- The receipt shows the SHA-256 digest of the policy file. A policy change
  is visible in the receipt chain.
- No policy file means no policy. A malformed policy file stops the run
  with an error. It never fails open.

## What correctful examines

| You write | correctful harvests | The probe | Tier |
|---|---|---|---|
| a Go test with an invariant name (`TestINV009_…`) | an invariant claim, attached to that test | `go test -json` events, not the exit code | T1 |
| an accept test and a reject test, as a pair | one adversarial claim | one `go test -json` run; the two tests must pass | T2 |
| a C# xUnit class with invariant names | a claim for each id, a probe for each `[Fact]` | `dotnet test --filter`, verdict from the counts | T1 |
| an Alloy `assert` with a `check` | a safety claim | the Alloy result file | T3 |
| an Alloy `run` | a witness claim (a pass shows that the model is consistent) | the Alloy result file | T3 |
| spec ids in shipped code (`INV-…`, `AP-…`, …) | a reference claim, anchored to its definition | id-named tests, with coverage proof | — |
| MUST clauses in a normative document | a must-clause claim | no probe is available — the claim stays in the remainder | T0 |
| nothing (`-llm`) | LLM proposals, with the mark `[llm-proposed]` | a changed test, only when the model names it and coverage confirms the link | T0 or T1 |

The tiers: **T0** unverified · **T1** one assertion held · **T2** an
accept/reject pair held · **T3** property or model check · **T4** proof,
exhaustive check, or observation.

## The design and the measurements

We measured each extractor and each probe on real repositories before
release. [DESIGN.md](DESIGN.md) contains the full design rationale, the
measured numbers, and the known limitations. The document shows the failures
and the successes.

## License

MIT.
