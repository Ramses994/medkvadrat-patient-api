# medkvadrat-patient-api

## Dev smoke test

On the dev stand (with a test phone and dev OTP), run:

```sh
./scripts/dev-smoke.sh http://localhost:8082 79935950695
```

The script must be executable (`chmod +x scripts/dev-smoke.sh`). It exercises OTP auth, `/api/me` and catalog reads, then books and cancels twice so the database ends with a normal cancelled (D) motconsu row.

`GET /api/me/profile` exposes `birth_date` (`YYYY-MM-DD`) from Medialog `PATIENTS.NE_LE` when set, otherwise `birth_year` from `GOD_ROGDENIQ`. Clients should show one or the other, not both.

## Integration tests (MSSQL)

Integration tests verify behavior against a real dev MSSQL instance. They cover write paths (book/cancel/restore) that mocks can't simulate — clinic-side triggers and stored procedures.

Prerequisites:
- Docker installed.
- `medkvadrat-internal` network exists (created by `docker compose up` on the deploy host).
- `.env` with `DB_SERVER`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` (same as api-gateway).

Run:

```sh
./scripts/integration-test.sh
```

The script runs `go test -tags=integration ./internal/integration/... -v` inside a throwaway `golang:1.22-alpine` container on `medkvadrat-internal` network and cleans up.

Override network if running outside compose context:

```sh
DOCKER_NETWORK=host ./scripts/integration-test.sh
```

See also: `docs/MEDIALOG_RULES.md`.

## Ad-hoc SQL tools (Medialog read-only)

From repo root with `.env` / env vars for MSSQL (same as the API):

```sh
go run ./tools/schemadiag                           # JSON: PAT% date columns + PATIENTS column list
go run ./tools/schemadiag verify-ne-le            # random sample: YEAR(NE_LE) vs GOD_ROGDENIQ
go run ./tools/schemadiag birthhunt               # optional env: BIRTHHUNT_PATIENTS_ID, BIRTHHUNT_DATE, …
```

In Docker, set `GATEWAY_DB_PATH=/app/data/gateway.db` and mount a volume on `/app/data` so SQLite is writable.
