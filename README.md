# medkvadrat-patient-api

Сборка и тесты: Go 1.27.x (см. `toolchain` в go.mod).

Документация ЛК и Medialog: в репозитории [medkvadrat-deploy](https://github.com/Ramses994/medkvadrat-deploy) каталог `docs/lk/`.

Переменные LK-0 (см. `.env.example`): `MEDIALOG_WRITE_*`, `DB_RW_*`, `MOTCONSU_POLL_ENABLED`, `CORS_ALLOWED_ORIGINS`, `DB_APP_NAME`. Read-логин по умолчанию `svc_patient_api_ro`.

## Dev smoke test

On the dev stand (with a test phone and dev OTP), run:

```sh
./scripts/dev-smoke.sh http://localhost:8082 79935950695
```

The script must be executable (`chmod +x scripts/dev-smoke.sh`). It exercises OTP auth, `/api/me` and catalog reads, including booking options and slots. It does not book or cancel. The process health check is `GET /api/health`.

`GET /api/me/profile` exposes `birth_date` (`YYYY-MM-DD`) from Medialog `PATIENTS.NE_LE` when set, otherwise `birth_year` from `GOD_ROGDENIQ`. Clients should show one or the other, not both.

## Web cabinet session (LK-1c)

Requests with `X-LK-Client: web` to `verify`, `select-patient`, `refresh` and `logout` use the cookie `lk_refresh` (`HttpOnly`, `SameSite=Strict`, `Path=/api/auth`, `Secure` unless `LK_COOKIE_SECURE=false`) for the refresh token; the body carries only `access` and `access_expires_in`. `refresh` and `logout` read the cookie and ignore a `refresh` field in the body; `logout` clears it. These requests must come from the same origin (`Origin` or `Referer` host equals `Host`), otherwise `403`. The web refresh token lives `LK_WEB_REFRESH_TTL_HOURS` (default `12`), rotates once and is accepted only from the web client. Without the header the responses are as before. Every `/api/auth/*` and `/api/me/*` response carries `Cache-Control: no-store`.

## Booking slots (LK-2a)

`GET /api/catalog/booking-options` and `GET /api/catalog/slots` (patient JWT) read free time from `pl_TelemedGetAvaliableSlots` for pairs in `LK_BOOKING_PAIRS`. The old `GET /api/schedule/slots` route is removed. `SLOT_TOKEN_SECRET` must be at least 32 bytes or slots and booking answer `503`. Times are Moscow wall-clock strings without an offset.

## Booking and cancel (LK-2b)

One API process holds the patient lock and then the line lock. Do not run a second instance against the same clinic lines.

`POST /api/me/appointments` with `{"slot_token":"…"}` and header `Idempotency-Key` (UUID) books through `dbo.pl_AddVisitContext`. `DELETE /api/me/appointments/{planning_id}` with the same header cancels through `dbo.pl_CancelVisit`. The old `POST /api/schedule/book` route and cancel by `motconsu_id` are removed.

Writes stay off unless `MEDIALOG_WRITE_ENABLED=true`, the patient and line are allowlisted, `DB_RW_USER` is set, `LK_AUTHOR_MEDDEP_ID` is set, and `@@LANGUAGE` on the RW login is `us_english`. The RW pool uses 2 open connections and 0 idle. A short or empty `SLOT_TOKEN_SECRET` makes booking answer `503`.

`GET /api/internal/write-ops?state=unknown,partial` and `POST /api/internal/write-ops/{op_id}/resolve` are API-token routes and also require a dashboard LAN address. Journal rows live in `gateway.db` (`lk_write_ops`). Unknown and partial rows are not retried.

Owner-only live check (changes the clinic database; run only on D1, never from CI):

```sh
LK_INTEGRATION_WRITE=1 go test -tags integration_write -count=1 -p 1 ./internal/integration/ -run TestLK2 -v
```

Allowlists must be exactly patient `1` and line `1119`, `LK_BOOKING_PAIRS` must include `1119:10000688`, and the RW login language must be `us_english`. The test refuses to start otherwise. It books only slots at least `CANCEL_MIN_HOURS_BEFORE` plus 2 hours ahead and cancels them through the same API.

## Запуск B7 владельцем

B7 — это `TestLK2`. Он пишет в клинику. Из CI его не запускают.

Перед стартом нужны `LK_INTEGRATION_WRITE=1`, `LK_LIVE_API_URL` (стенд API) и `LK_LIVE_API_TOKEN` (внутренний токен стенда). Тест вызывает `GET /api/internal/write-ops?state=in_flight,unknown,partial` (тайм-аут 10 с). Стартует **только** при ответе `200` с `{"success":true,"data":[]}`; непустой список, `data` нет или `null`, `success:false`, неразборчивый ответ, ошибка или тайм-аут — тест не стартует. Ключ отмены создаётся один на `planning_id` до первого `DELETE` и используется и в сценарии, и в очистке.

Чтение до сценариев: у пациента `1` на линии `1119` нет действующих будущих записей. Слоты берутся не раньше `сейчас + CANCEL_MIN_HOURS_BEFORE + 2 часа`. Иначе тест не стартует.

Списки допуска — ровно пациент `1` и линия `1119`, пара `1119:10000688`, язык RW-логина `us_english`.

Первая ошибка сценария останавливает остальные. Записанные `planning_id` всё равно уходят в очистку. Отмена повторяется с тем же ключом идемпотентности. Если ответ «уже отменена» или `CANCEL_NOT_SUPPORTED`, тест проверяет чтением, что запись отменена или строки нет.

Лог обработчика и `gateway.db` остаются в каталоге `./b7-run-<время>/`. Путь печатается в начале и в конце.

После очистки тест ещё раз читает записи пациента `1` на линии `1119` с автором ЛК, созданные после старта. Действующая запись — ошибка со списком `planning_id` и инструкцией: Расписание → линия 1119 → запись → «Пациент не придёт».

## Staff dashboard

Internal schedule board for three branches (staff session, not `API_TOKEN`):

- Page: `GET /dashboard` (login at `/dashboard/login`)
- Data: `GET /dashboard/data?date=YYYY-MM-DD` (same JSON as `GET /api/dashboard/schedule`)

Env: `DASHBOARD_PASSWORD`, `DASHBOARD_SECRET` (`openssl rand -hex 32`), optional `DASHBOARD_SESSION_TTL` (default `12h`), `DASHBOARD_COOKIE_SECURE` (default `false` for LAN HTTP), `DASHBOARD_ALLOWED_CIDRS` (default `192.168.0.0/16,127.0.0.1/32`).

Stand (LAN): `http://192.168.2.104:<API_PORT>/dashboard`. `API_TOKEN` must never appear in the page/JS — the browser uses the `mk_staff` cookie only.

S2S clients keep using `GET /api/dashboard/schedule` with `Authorization: Bearer <API_TOKEN>`.

## Integration tests (MSSQL)

The Docker script below runs the read-only `integration` tag. It does not book. The live book/cancel check is the `integration_write` command in the booking section; that one changes the clinic database and is owner-only.

Prerequisites:
- Docker installed.
- `medkvadrat-internal` network exists (created by `docker compose up` on the deploy host).
- `.env` with `DB_SERVER`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` (same as api-gateway).

Run:

```sh
./scripts/integration-test.sh
```

The script runs `go test -tags=integration ./internal/integration/... -v` inside a throwaway `golang:1.27-alpine` container on `medkvadrat-internal` network and cleans up.

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
