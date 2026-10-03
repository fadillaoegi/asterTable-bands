# NightWatch Factory — Design and Results

The NightWatch Factory is a four-seat software factory operated inside a Band room. It turns a
complete specification into an independently verified, containerized service, with review and
adversarial verification that actually gate the result.

## Seats

| Seat | Owns | Must not do |
|---|---|---|
| Coordinator | Requirement matrix, assignments, evidence gates, final report | Write production code or self-certify |
| Implementer | Scoped implementation, tests, packaging, repair commits | Approve its own work |
| Conformance Reviewer | Independent specification comparison and acceptance checks | Write production code |
| Adversarial Verifier | Black-box, concurrency, failure, DST, browser, and clean-environment campaigns | Repair production code |

All seats run on the Codex harness with model `gpt-5.6-sol`. Their detailed, domain-independent
rules live in `mandates/`. Mandates never name the track, endpoints, fields, status codes, or
domain details; those live only in the task/spec the Coordinator pastes into each handoff.

## Workflow

```text
complete specification
  -> requirement matrix and owned work
  -> committed implementation handoff
  -> independent conformance review
  -> adversarial verification
  -> rejection and committed repair when needed
  -> isolated acceptance run (official harness, clean container)
  -> evidence-backed coordinator report
```

Every handoff names the covered requirement IDs, changed files, the full commit, commands and
results, a PASS/FAIL decision, residual risks, elapsed time and model usage/cost, and one
explicit next owner. Assignments and replies use literal `@handle` addressing in both directions.
No gate is skipped because an earlier check was green; a passing partial harness is evidence,
not proof that withheld cases are correct.

## Failure handling

A failure is returned to the Implementer through the Coordinator with a reproducible case and a
minimal acceptance criterion. The Implementer repairs the underlying invariant — not the
symptom — and adds regression coverage in a new commit. The Reviewer or Verifier, never the
Implementer, reruns the reproduction and the relevant earlier checks against the new commit.

### Rehearsal evidence (toy track) — the loop demonstrably gates results

The toy rehearsal exercised the full loop end-to-end on Stage 1. The Adversarial Verifier
rejected commit `a4d7569` because `DELETE /counter` returned `501` (a 5xx), violating the
no-5xx invariant. The Implementer repaired it in `7f2bb9d` (unsupported methods → `405`
JSON, other 5xx → `400` JSON). Clean re-verification passed: official harness 8/8 with
`claimed stage 1`, and a full adversarial campaign with `failures: []` over 2,396 requests.
This proves review and verification change the outcome, not just approve it.

## Measured results (final run — fill from room + harness artifacts, do not estimate)

- elapsed wall time: _pending final run_
- model usage/cost: _pending room export_
- commits (implementation + repairs): _pending final run_
- rejected-and-repaired work: _pending final run_
- highest stage claimed: _pending harness run_
- isolated cold-start result (<60 s, <2 GiB, offline): _pending harness run_

## Invariant enforcement

The no-double-booking invariant is enforced at the database layer with a deferrable GiST
exclusion constraint (`btree_gist`) over `(restaurant_id, table_id, conflict_range)`, where
`conflict_range` is the half-open `tstzrange [starts_at, ends_at)` while an assignment is
active and NULL once released. Multi-reservation moves set the constraint deferred so the
final transaction state is validated atomically, never an intermediate swap state.
