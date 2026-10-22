# Integration tests

Только чтение. Запускает **владелец** под `svc_patient_api_ro`. Агент к боевому Медиалогу не подключается.

`notify_queries_test.go` (тег `integration`) вызывает `DueReminders`, `NewBookings` (курсор «сейчас минус 30 дней»), чтение одной записи `BookingByPlanningID` и `DashboardPlanningRows` для `patient_ids=1,1587578`. Проверяет отсутствие ошибки и что SQL без `INSERT|UPDATE|DELETE|MERGE|EXEC`. Данные (ПДн) в вывод не печатаются.

```text
set INTEGRATION_NOTIFY_QUERIES=1
go test -tags=integration ./internal/integration -run TestNotifyQueries_ReadOnlyLive -count=1
```

Нужны переменные подключения RO (`DB_SERVER`, `DB_NAME`, `DB_USER=svc_patient_api_ro`, `DB_PASSWORD`) в окружении или `.env`.

Старый `booking_concurrency_test.go` не используется для записи.
