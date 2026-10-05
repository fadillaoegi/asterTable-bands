# AsterTable — NightWatch Factory

**Team:** FLdevBoo · **Members:** Fadil Laoegi · **Track:** `tablekeeper`
**Competition:** WeAreDevelopers × BAND — Dark Factory

**AsterTable** is a restaurant reservation service. Diners search availability, book a table,
receive a stable confirmation reference, and look up, amend, cancel, or batch-move their
bookings — safely under retries and concurrent load. The core invariant: two `confirmed`
reservations never occupy the same table over overlapping time, even when up to 50 requests
arrive together.

*Every table. One truthful booking.*

## How to read this repository

| Path | What it is |
|---|---|
| `FACTORY.md` | **Start here.** The factory: seats, workflow and gates, how it caught bad work, design choices and costs, measured time and spend, what failed, and every human action during the run |
| `mandates/` | Generic, domain-independent rules for the four seats (Coordinator, Implementer, Conformance Reviewer, Adversarial Verifier). Each names its harness and model |
| `room.json` | The unchanged full-session export of the Band room the factory worked in |
| `stage-1/` | Stage 1 — the reservation JSON API, as last committed by the seats (`b637b81`). Official harness, isolated mode: Stage 1 120/120. One open finding from the factory's own review is documented in `FACTORY.md` §3 and §7 |

Every `stage-N/` folder is a complete service (source, `Dockerfile`, `RUN.md`) produced by the
seats inside the Band room, and claims only its own stage. Human-authored content is limited to
`README.md`, `FACTORY.md` and `mandates/`. The git history is the seats' history, unedited, followed by
one documentation commit by the owner.

## Run a stage

From the stage folder, follow its `RUN.md` (one command builds and starts the service). The image
listens on `0.0.0.0:$PORT` (default 8080), starts PostgreSQL 18 internally, needs no outbound
network at runtime, and becomes healthy within 60 s.

## Verify

From the organizers' kickoff checkout, with its Python environment:

```sh
python -m harness check --track tablekeeper <path-to-this-repo>
python -m harness run --track tablekeeper --repo <path-to-this-repo> --all --mode isolated
```

## Technology

Go 1.26 (`net/http` + chi) · `pgx/v5` · PostgreSQL 18 embedded in one runtime image
(`btree_gist`, deferrable GiST exclusion constraint for the no-double-booking invariant) ·
bcrypt password hashing · IANA tzdata for time zones and DST.
