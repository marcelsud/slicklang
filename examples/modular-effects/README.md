# Modular effects

This runnable shipment reconciliation example separates compiler-checked authority from application dependencies:

```text
main -> adapters -> application ports <- application workflow -> domain
```

- `domain/` owns values and typed application failures. It is pure and has no `std.*` calls.
- `application/` owns cohesive ports and `Reconcile`. The workflow knows no concrete adapter or standard-library namespace and declares the union of the port effects: `database`, `filesystem`, and `network`.
- `adapters/` owns filesystem, HTTP, JSON, and SQLite details. Every host failure becomes a safe `AppFailure` before crossing a port.
- `main.slk` reads configuration, selects implementations, opens SQLite, and owns its `using` lifetime.

## Run

Start an HTTP service that returns one of these bodies from `GET /<shipment-id>`:

```json
{"decision":"accepted"}
```

Then run:

```sh
MODULAR_EFFECTS_INPUT=examples/modular-effects/fixtures/shipments.json \
MODULAR_EFFECTS_RISK_URL=http://127.0.0.1:8081 \
MODULAR_EFFECTS_RISK_TOKEN=example-token \
MODULAR_EFFECTS_DATABASE=/tmp/modular-effects.db \
MODULAR_EFFECTS_REPORT=/tmp/modular-effects-report.json \
MODULAR_EFFECTS_NOW_EPOCH=1700000000 \
go run ./cmd/slick run examples/modular-effects
```

The maintained integration test supplies the deterministic loopback service and verifies both the interpreter and a generated native binary.

## When a port earns an interface

Use a port when it represents a cohesive application capability with independently replaceable policy or resource ownership: loading a batch, assessing risk, storing reconciliations, supplying the observed epoch, or writing the audit report. Put the effect contract on that port so interface dispatch stays conservative; an implementation may use a narrower set.

Call `std.*` directly inside a cohesive adapter. Do not add `FileSystem`, `HTTPTransport`, or `SQLiteExecutor` interfaces merely to rename one host call. Pure domain functions and value construction do not need interfaces. Keep native resource lifetimes concrete in the composition root rather than hiding them behind a port.

`ConfiguredClock` is deliberately pure because composition supplies the epoch. Replacing it with a host clock requires adding `time` to `Clock.NowEpoch`, `Reconcile`, and the composition root; the compiler then makes that authority change visible at every contract boundary.
