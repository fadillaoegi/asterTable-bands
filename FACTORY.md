# NightWatch Factory

NightWatch Factory is a four-seat software factory run in a Band Desktop room. It turns a complete
written specification into an independently reviewed, adversarially verified, containerized service.
Its central rule: **no seat accepts its own work**, and a stage advances only on evidence produced by
a seat that did not write the code.

This document is enough to stand the factory up again: seats and setup, the workflow and gates,
design choices and their costs, what failed and how we changed, and measured time and model spend.

---

## 1. Seats

| Seat | Owns | Must not do |
|---|---|---|
| **Coordinator** | Requirement matrix, assignments, evidence gates, stage acceptance, final report | Write production code; accept a handoff without evidence |
| **Implementer** | Scoped implementation, tests, packaging (`Dockerfile`, `RUN.md`), repair commits | Approve its own work or mark a stage accepted |
| **Conformance Reviewer** | Independent comparison of the exact committed revision against the complete spec; acceptance harness | Write production code |
| **Adversarial Verifier** | Black-box, concurrency, retry, fault, restart, offline and resource-limit campaigns with preserved evidence | Repair production code |

All four seats run the **Codex** harness with model **`gpt-5.6-sol`**, each as a Band-owned runtime.
The mandates in `mandates/` are domain-independent: they describe how a seat works, hands off and
rejects, and never name this track, its endpoints, fields or error codes. Everything specific to the
problem travels in the task the Coordinator pastes into each handoff.

### Setup (to reproduce)
1. Band Desktop ≥ 0.4.12, Docker daemon, Python 3.12+ for the event harness.
2. Create four Band-owned seats named exactly after the mandate files and paste each mandate as the
   seat's instructions. Give the Reviewer and Verifier a runtime that can reach the Docker socket.
3. Initialise an empty result repository and give the seats its **absolute** path.
4. Create one fresh room, add all four seats, and send the Coordinator one stage dispatch containing
   the workspace paths, the spec path, the locked product stack and the harness command template.
5. Send nothing else until the Coordinator's final report.

---

## 2. Workflow and gates

```text
dispatch (complete spec)
  → Coordinator: requirement matrix (owner + verification + evidence path per requirement)
  → Implementer: committed revision + handoff
  → Conformance Reviewer: exact-revision review + harness ── FAIL ─┐
  → Adversarial Verifier: hostile campaigns ─────────────── FAIL ─┤
  → Implementer: repair of the invariant + regression test ◀──────┘ (new commit, back through review)
  → isolated acceptance run (official harness, no network, 2 vCPU / 2 GiB)
  → Coordinator: stage accepted, evidence receipt
```

Every handoff uses one compact structure: requirement IDs, files changed, full committed revision,
commands and results, explicit **PASS/FAIL**, residual risks, elapsed time and model usage, and one
next action with an `@owner`. Assignments, rejections and repairs use literal `@handle` addressing.

Gates the Coordinator enforces:
- A handoff missing a committed revision, commands/results or a decision is rejected.
- A green partial harness is evidence, not proof: the shipped checks are a fraction of the graded
  suite, so the Reviewer and Verifier also test what the spec says and the checks never ask.
- A repair always returns to review on its **exact new revision**; earlier evidence is not inherited.

---

## 3. How the factory catches bad work — real cases from the submitted run

Every case below was found by a seat that did not write the code, and fixed by the Implementer in a
new commit that went back through review. The official visible harness was **already green** for
several of these revisions; the defects came from reading the spec, not the tests.

| # | Revision | Found by | Defect | Repair |
|---|---|---|---|---|
| 1 | `5ab071e` | independent harness / Reviewer | After export → import, a reservation's offset changed from `+02:00` to `+00:00` (119/120) | `fe74795` — preserve restaurant offset; 120/120 |
| 2 | `fe74795` | Conformance Reviewer | Import joined tables without the composite restaurant/table identity; string fields accepted JSON `null` | `15b5f8b` |
| 3 | `15b5f8b` | Conformance Reviewer | Local-time resolution used a whitelist of minute offsets and **rejected a valid historical Europe/Amsterdam 1938 time (+00:20)**, harness still 120/120 | `6578dda` — resolve wall times from the IANA rules themselves |
| 4 | `6578dda` | Conformance Reviewer | Importing state with an invalid restaurant timezone (`No/Such_Zone`) returned **204 and partially replaced** the destination; spec requires 422 and no change. Harness 120/120 | `7448429` — validate before any replacement |
| 5 | `7448429` | Adversarial Verifier | A semantic import fuzzer found four internally inconsistent exports accepted with 204 (timezone changed without matching times; local start changed alone; duration changed without end; party above table capacity). **Isolated harness 120/120** | `f4d40cc` — semantic preflight before replacement; Reviewer PASS |
| 6 | `f4d40cc` | Adversarial Verifier | Ten waves of 50 simultaneous valid availability requests at 2 vCPU / 2 GiB: **500/500 timed out past 5 s** (cold-date work serialized). Isolated harness 120/120 | `b637b81` — remove cold availability serialization; two 1,500-request constrained runs then passed |
| 7 | `b637b81` | Conformance Reviewer | Mixed load — availability reads overlapping reset/import/export — deadlocked: **66 PostgreSQL deadlocks, 81 rollbacks**, valid calls timing out or answered `400`. Isolated harness 120/120 | **Not repaired**: the repair was in progress when the provider's usage limit stopped all seats (§6) |

Rehearsal (toy track): the Adversarial Verifier rejected a revision because `DELETE /counter`
returned a 5xx, although the official harness passed 8/8. The Implementer repaired it and a clean
re-verification passed with no failures.

---

## 4. Design choices and what they cost

| Choice | Why | Cost |
|---|---|---|
| Four seats with strict non-self-review | Independent eyes caught four real defects the visible checks missed | More handoffs and tokens per stage; the Reviewer was the most expensive seat |
| Coordinator writes no code | Keeps acceptance decisions separate from implementation | One seat's spend produces no code directly |
| Invariants at the strongest layer (database exclusion constraint for "no double booking") | Concurrency correctness does not depend on application timing | Requires PostgreSQL inside the single image |
| One stage per dispatch | A broken earlier stage caps every later one; each stage is accepted before widening | Slower than dispatching all stages at once |
| Locked product stack in the dispatch, generic mandates | Mandates stay reusable for any problem; the dispatch carries the product decision | The dispatch is long |
| Isolated-mode harness as the final gate | Matches judging: no outbound network, 2 vCPU, 2 GiB | First isolated run is slow (runner image build) |

---

## 5. Measured time and model spend

Band's per-room estimate at list prices (not a provider bill), from `band usage rooms`:

| Room | Sessions | Tokens | Estimate | Per seat |
|---|---:|---:|---:|---|
| Final run (Stage 1, Stage 2 queued) | 13 | ~78.5 M | **$46.32** | Implementer $19.64 · Reviewer $17.48 · Verifier $4.81 · Coordinator $4.39 |
| Toy rehearsal | 5 | ~78.7 M | $41.85 | Coordinator $20.23 · Reviewer $13.59 · Implementer $5.42 · Verifier $2.60 |

Timeline of the submitted run (WIB, UTC+7):

| Time | Event |
|---|---|
| 10-03 16:46 | Stage 1 dispatched; Coordinator hands the complete spec to the Implementer at 16:48 |
| 10-03 17:07–22:45 | Seats paused by provider usage limits (see §6) |
| 10-03 23:00 | `5ab071e` first complete Stage 1 |
| 10-03 23:08 / 23:18 | `fe74795`, `15b5f8b` repairs after review |
| 10-04 01:47 | `6578dda` repair (historical IANA offsets) |
| 10-04 22:45 | `7448429` repair (invalid-timezone import) |
| 10-04 22:57 | Conformance Reviewer PASS on `7448429`; Coordinator dispatches the Verifier |
| 10-05 07:26 | Adversarial verification resumes after provider credits return |
| 10-05 07:31 | Verifier FAIL on `7448429` (semantic import fuzzing) → Coordinator assigns repair |
| 10-05 08:08 | `f4d40cc` repair committed; 08:28 Conformance Reviewer PASS; 08:29 Verifier dispatched |
| 10-05 08:40–19:48 | Seats paused: provider credits exhausted mid-verification |
| 10-05 19:56 | **Stage 2 dispatched** (second and last human task message; instructs the Coordinator to finish Stage 1 first) |
| 10-05 19:57 | Coordinator: Stage 2 "queued behind the hard Stage 1 acceptance gate" |
| 10-05 20:03 | Verifier FAIL on `f4d40cc` (latency under 50 simultaneous requests) → 20:05 repair assigned |
| 10-05 20:21 | `b637b81` repair committed; 20:32 Conformance re-review assigned |
| 10-05 21:22 | Conformance Reviewer FAIL on `b637b81` (deadlocks under mixed state replacement) → 21:25 repair assigned |
| 10-05 21:37 | Provider usage limit reached for all seats until 10 Oct, after the deadline; the owner stopped the run |

### Outcome
- **Stage 1** is submitted at `b637b81`. It builds and serves from a clean container through
  `RUN.md`, and the official harness in isolated mode reports Stage 1 **120/120, claimed stage 1**.
  It was **not accepted by the factory's own gates**: finding 7 is open. We submit it with that
  finding stated rather than relaxing a gate to call it done.
- **Stage 2** was dispatched but never started: the Coordinator held it behind the Stage 1 gate,
  as instructed. No `stage-2/` folder is submitted.
- The Implementer's unfinished, uncommitted deadlock repair is not part of the submission.

Independent isolated-mode check of `7448429` (no network, 2 vCPU, 2 GiB): Stage 1 **120/120**,
`claimed stage: 1`; the Stage 2 overshoot suite fails as it should.

---

## 6. What failed, and what we changed

- **Seats without Docker access (rehearsal).** Workspace-write seats could not reach the Docker
  socket, so the Reviewer correctly reported FAIL instead of passing on unit tests alone. We gave the
  verification seats a Docker-capable runtime.
- **Provider usage limits.** The seats' Codex workspace repeatedly ran out of credits or hit usage
  limits during the run (every occurrence is an `error` event in `room.json`), stopping seats mid-task. The factory itself does not route around this; we
  tested an automatic provider failover and did not adopt it, because switching a live Band session's
  runtime failed and the mandates would no longer state the model a seat actually ran.
- **Delivery after restart.** After a limit, restarting a seat's room session sometimes reached
  `ready` without starting a turn and was then stopped as idle. Restarting a session whose last turn
  had *failed* reliably resumed that turn. Messages addressed to a seat while it was dormant were not
  always delivered; restarting the local Band daemon (Band Desktop) delivered the missed handoff and the
  seat resumed on its own.

### Human actions during the submitted run (disclosure)
We list every human action after dispatch so the run can be judged accurately:
- The task messages are the Stage 1 dispatch and the Stage 2 dispatch (sent 2026-10-05 19:56 WIB,
  instructing the Coordinator to finish Stage 1 under its own gates before starting Stage 2).
- When seats stopped on provider limits, the owner, or a coding assistant acting for the owner,
  **restarted the affected seats' existing room sessions**, and once restarted the local Band
  daemon so missed handoffs were delivered (no message, no instruction). This is the full extent of the intervention for the
  limit and delivery stalls; the seats continued from the handoffs already in the room.
- On 2026-10-05 at 01:48 WIB the owner posted one message, `test`, addressed to the Adversarial
  Verifier while diagnosing the stalled session. It carried no instruction or information about the
  work.
- No human reviewed, edited or committed code under `stage-*/`.
- At 2026-10-05 21:37 WIB the provider's usage limit stopped every seat until after the deadline;
  the owner then ended the run. The documentation commit (`README.md`, `FACTORY.md`, `room.json`)
  is the owner's, made on top of the seats' unedited history.

---

## 7. Residual risks

- The shipped checks are partial; spec areas not covered by them rely on the Reviewer's and
  Verifier's own campaigns.
- Throughput depends on the model provider's limits; a long stall delays a stage but does not
  change its gates.
- **Open defect (finding 7):** reset, import or export running at the same moment as ordinary
  reads can deadlock in PostgreSQL, so some of those requests time out or are rejected with `400`.
  Ordinary bookings, reads and the standalone 50-request load pass on the submitted revision.
- No continuous peak-memory trace was captured during the failing load waves (post-wave memory
  was about 178 MiB against the 2 GiB limit).
