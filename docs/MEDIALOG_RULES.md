# Правила работы с базой Medialog

Medialog — продакшн‑БД клиники. Наша интеграция должна быть максимально “прозрачной” для клиники и совместимой с их триггерами/процедурами.

## Запрещено без явного согласования с клиникой

- Любые `ALTER/CREATE/DROP` (таблицы/индексы/триггеры/процедуры/функции) в Medialog.
- Вносить изменения в HL7/MOBIMED служебные таблицы напрямую (если это не делает сама Medialog через свои SP/триггеры).
- Оборачивать clinic-side stored procedures (например, `CreateMotconsu`) во внешнюю транзакцию из нашего кода. На реальных инсталляциях триггеры могут abort’ить outer transaction.
- Поднимать уровень изоляции выше `READ COMMITTED` в пользовательском коде.
- Использовать table hints (`UPDLOCK`, `HOLDLOCK`, `TABLOCK`, …) как способ “исправить гонки”.

## Разрешено

- `SELECT` для read-path’ов.
- `EXEC` существующих clinic-side stored procedures (например, `CreateMotconsu`) **как контракт клиники**, без внешней транзакции.
- Write‑логика сервиса (OTP/JWT/sessions/rate limit/напоминания) — в нашей SQLite (`gateway.db`), не в Medialog.

## Паттерн “guarded UPDATE”

Для конкуренции (гонок) используем атомарный UPDATE с предусловием:

```sql
UPDATE target
SET ...
WHERE id = @id AND <business_invariant>
```

Дальше проверяем `RowsAffected()`:
- `1` — успех,
- `0` — предусловие нарушено / слот уже занят → возвращаем `409 SLOT_TAKEN` или аналогичный бизнес‑код.

## Integration‑тесты обязательны для Medialog write‑path

Golden/unit тесты через моки **не ловят**:
- триггеры,
- поведение stored procedures,
- реальные ограничения и побочные эффекты в MSSQL.

Каждый write‑эндпоинт в Medialog должен иметь integration‑тест под build‑tag `integration` и прогоняться на dev MSSQL через `./scripts/integration-test.sh`.

## Персональные данные в логах

Правило того же уровня, что read-only по Medialog. Применяется ко **всем** PR и окружениям (prod, стенд, локально).

### Обязательно

- В логи (`slog`, stdout, middleware) — **только идентификаторы**: `patient_id`, `planning_id`, `motconsu_id`, `doctor_id`, `branch_id`, `request_id`, `status`, `latency_ms`, `err`.
- ФИО (`NOM`/`PRENOM`, `patient_name`, `doctor_name`, `full_name`), телефон, email, адрес, дата рождения — **никогда** в логах.
- ПДн допустимы **только в теле HTTP-ответа** авторизованному клиенту по контракту эндпоинта (staff/S2S, JWT пациента).
- Handler-ошибки логировать как `err` + id из контекста запроса, **без** сериализации DTO/`data`.
- Read-path SQL не джойнить `PATIENTS`/`MEDECINS` ради имён, если имена нужны только для логов или отладки — убрать данные из выборки, а не из формата лога.
- **Не запускать** `patient-api` и diag-утилиты против боевого Medialog (`192.168.2.229`) из облачных агентов (Cursor Cloud и т.п.) — риск утечки ПДн в лог агента.

### Исключения

- Маскированные значения, если без них нельзя отладить (например `masked_email` в OTP audit).
- `AUTH_MODE=dev`: OTP-код в лог допустим только на изолированном dev-инстансе.

### Локальные diag-утилиты

`tools/planningdiag/` — read-only (`SELECT` only), не в git (`.gitignore`). Вывод в stdout может содержать ПДн; не коммитить, не гонять на проде.

