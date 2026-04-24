# Integration tests (MSSQL)

Tests are behind `//go:build integration` so `go test ./...` does not run them.

```bash
# Against dev (or any) Medialog; leaves one booked slot — use a test stand only.
set INTEGRATION_MSSQL=1
# DB_* and API_TOKEN as in your .env
go test -tags=integration -v ./internal/integration/...
```

For CI, you can add a job with `-tags=integration` and DB secrets, or start SQL Server in Docker and load a minimal Medialog (usually not worth it; prefer dev-MSSQL).

Reference image: `mcr.microsoft.com/mssql/server:2022-latest`.
