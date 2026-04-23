# medkvadrat-patient-api

## Dev smoke test

On the dev stand (with a test phone and dev OTP), run:

```sh
./scripts/dev-smoke.sh http://localhost:8082 79935950695
```

The script must be executable (`chmod +x scripts/dev-smoke.sh`). It exercises OTP auth, `/api/me` and catalog reads, then books and cancels twice so the database ends with a normal cancelled (D) motconsu row.

`GET /api/me/profile` exposes `birth_year` (integer) parsed from Medialog `GOD_ROGDENIQ` (year in varchar), not a synthetic `birth_date`. In Docker, set `GATEWAY_DB_PATH=/app/data/gateway.db` and mount a volume on `/app/data` so SQLite is writable.
