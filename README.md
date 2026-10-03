# AsterTable — NightWatch Factory

**AsterTable** is a restaurant reservation service. Diners search availability, book a table
(or an approved combination), receive a stable confirmation reference, and look up, amend,
cancel, or batch-move their bookings — safely under retries and concurrent load. The core
invariant: two `confirmed` reservations never occupy the same table over overlapping time,
even when up to 50 requests arrive together.

*Every table. One truthful booking.*

This repository is the **graded submission** for the WeAreDevelopers × BAND Dark Factory
competition, track `tablekeeper`. The service code in each `stage-*/` folder is produced by
the four seats of the **NightWatch Factory** inside a Band room. Human-authored content is
limited to the factory documents, the generic seat mandates, and the organizer-provided
starting scaffold. Hand-built production code outside the room is not part of the deliverable.

## Layout

- `mandates/`: generic, domain-independent operating rules for the four seats
  (Coordinator, Implementer, Conformance Reviewer, Adversarial Verifier).
- `stage-1/` … `stage-4/`: cumulative, independently buildable services produced by the room.
  Each stage folder is a complete service with source, `Dockerfile`, and `RUN.md`; each claims
  only its own stage. `stage-2/` extends a copy of `stage-1/`, and so on.
- `FACTORY.md`: how the factory is organized, how it works, and its measured results.
- `room.json`: the unchanged full-session export of the Band room, added after the run.

## Technology

Go 1.26 (`net/http` + chi) · `pgx/v5` · **PostgreSQL 18** embedded in a single runtime image
(`btree_gist`, deferrable GiST exclusion constraint for the no-double-booking invariant) ·
React 19 + TypeScript (from Stage 2) · bcrypt password hashing · IANA tzdata for DST.
The image listens on `0.0.0.0:$PORT` (default 8080), starts PostgreSQL internally, needs no
outbound network at runtime, and becomes healthy within 60 s.

## Verification

Run from the official kickoff checkout at `/Users/boo/Github/project/astertable/kickoff`
with its `.venv`:

```sh
# per-stage acceptance
./.venv/bin/python -m harness run --track tablekeeper --repo <this-repo> --stage N --out <out-dir>
# offline eligibility check
./.venv/bin/python -m harness check --track tablekeeper <this-repo>
```

Each stage must report `claimed stage: N`, and the offline check must report no problems.
