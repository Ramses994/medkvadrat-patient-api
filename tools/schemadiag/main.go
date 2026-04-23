// schemadiag runs read-only Medialog INFORMATION_SCHEMA probes (birth-related columns).
// go run ./tools/schemadiag
//
// Prints one JSON object to stdout: pat_date_columns, patients_columns, errors.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	m, err := db.OpenMSSQL(cfg.MSSQL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mssql:", err)
		os.Exit(1)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	out := make(map[string]any)

	rows, err := m.QueryContext(ctx, `
SELECT t.TABLE_NAME, c.COLUMN_NAME, c.DATA_TYPE
FROM INFORMATION_SCHEMA.TABLES t
JOIN INFORMATION_SCHEMA.COLUMNS c ON c.TABLE_NAME = t.TABLE_NAME AND c.TABLE_SCHEMA = t.TABLE_SCHEMA
WHERE t.TABLE_TYPE = 'BASE TABLE'
  AND t.TABLE_NAME LIKE '%PAT%'
  AND c.DATA_TYPE IN ('date','datetime','datetime2','smalldatetime')
ORDER BY t.TABLE_NAME, c.COLUMN_NAME`)
	if err != nil {
		out["pat_date_columns_error"] = err.Error()
	} else {
		var list []map[string]string
		for rows.Next() {
			var table, col, typ string
			_ = rows.Scan(&table, &col, &typ)
			list = append(list, map[string]string{"table_name": table, "column_name": col, "data_type": typ})
		}
		_ = rows.Close()
		out["pat_date_columns"] = list
	}

	rows, err = m.QueryContext(ctx, `
SELECT COLUMN_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_NAME = 'PATIENTS'
ORDER BY ORDINAL_POSITION`)
	if err != nil {
		out["patients_columns_error"] = err.Error()
	} else {
		var plist []map[string]any
		for rows.Next() {
			var name, dtype string
			var maxlen sql.NullInt64
			_ = rows.Scan(&name, &dtype, &maxlen)
			row := map[string]any{"column_name": name, "data_type": dtype}
			if maxlen.Valid {
				row["character_maximum_length"] = maxlen.Int64
			} else {
				row["character_maximum_length"] = nil
			}
			plist = append(plist, row)
		}
		_ = rows.Close()
		out["patients_columns"] = plist
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
