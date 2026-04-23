// schemadiag — read-only Medialog SQL helpers for schema / birth-field checks.
//
//	go run ./tools/schemadiag              — default: INFORMATION_SCHEMA dumps (JSON)
//	go run ./tools/schemadiag verify-ne-le — 10 random rows: NE_LE year vs GOD_ROGDENIQ
//	go run ./tools/schemadiag birthhunt    — probe PATIENTS (see -h via first arg)
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "verify-ne-le":
			runVerifyNeLe()
			return
		case "birthhunt":
			runBirthhunt()
			return
		case "-h", "--help", "help":
			fmt.Fprintln(os.Stderr, "usage: go run ./tools/schemadiag [verify-ne-le|birthhunt]")
			os.Exit(0)
		}
	}
	runDefaultSchemaDump()
}

func openMSSQL(ctx context.Context) (*sql.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return db.OpenMSSQL(cfg.MSSQL)
}

func runVerifyNeLe() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m, err := openMSSQL(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer m.Close()

	q := `
SELECT TOP 10 PATIENTS_ID, NOM, PRENOM,
       NE_LE, GOD_ROGDENIQ,
       YEAR(NE_LE) AS ne_le_year,
       CASE WHEN TRY_CAST(GOD_ROGDENIQ AS INT) = YEAR(NE_LE)
            THEN 'match' ELSE 'MISMATCH' END AS chk
FROM PATIENTS
WHERE NE_LE IS NOT NULL
  AND GOD_ROGDENIQ IS NOT NULL AND GOD_ROGDENIQ <> ''
ORDER BY NEWID()`
	rows, err := m.QueryContext(ctx, q)
	if err != nil {
		fmt.Fprintln(os.Stderr, "query:", err)
		os.Exit(1)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var id int64
		var nom, pre sql.NullString
		var neLe time.Time
		var god string
		var y int
		var chk string
		if err := rows.Scan(&id, &nom, &pre, &neLe, &god, &y, &chk); err != nil {
			fmt.Fprintln(os.Stderr, "scan:", err)
			os.Exit(1)
		}
		row := map[string]any{
			"patients_id": id,
			"ne_le":       neLe.Format(time.RFC3339),
			"god_rogdeniq": god,
			"ne_le_year":  y,
			"check":       chk,
		}
		if nom.Valid {
			row["nom"] = nom.String
		}
		if pre.Valid {
			row["prenom"] = pre.String
		}
		out = append(out, row)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func runDefaultSchemaDump() {
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

func runBirthhunt() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	m, err := openMSSQL(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer m.Close()

	out := make(map[string]any)

	ln := getenv("BIRTHHUNT_LAST_NAME", "ШМЕЛЕВА")
	fn := getenv("BIRTHHUNT_FIRST_NAME", "Ольга")
	dateStr := getenv("BIRTHHUNT_DATE", "1962-01-05")
	if _, err := time.Parse("2006-01-02", dateStr); err != nil {
		fmt.Fprintln(os.Stderr, "BIRTHHUNT_DATE must be YYYY-MM-DD")
		os.Exit(1)
	}
	var pid int64
	if s := os.Getenv("BIRTHHUNT_PATIENTS_ID"); s != "" {
		var err error
		pid, err = strconv.ParseInt(s, 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "BIRTHHUNT_PATIENTS_ID must be int")
			os.Exit(1)
		}
	}

	var tableType string
	_ = m.QueryRowContext(ctx, `SELECT TABLE_TYPE FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME = 'PATIENTS'`).Scan(&tableType)
	out["patients_table_type"] = tableType

	var def sql.NullString
	_ = m.QueryRowContext(ctx, `SELECT OBJECT_DEFINITION(OBJECT_ID('dbo.PATIENTS'))`).Scan(&def)
	if def.Valid {
		s := def.String
		if len(s) > 4000 {
			s = s[:4000] + "…(truncated)"
		}
		out["patients_object_definition"] = s
	} else {
		out["patients_object_definition"] = nil
	}

	if pid == 0 {
		rows, err := m.QueryContext(ctx, `
SELECT TOP 10 PATIENTS_ID, NOM, PRENOM, PATRONYME, GOD_ROGDENIQ, NOMER_MEDICISKOJ_KART
FROM PATIENTS
WHERE NOM LIKE @nom AND PRENOM LIKE @fn`,
			sql.Named("nom", ln+"%"),
			sql.Named("fn", fn+"%"),
		)
		if err != nil {
			out["patient_search_error"] = err.Error()
		} else {
			var found []map[string]any
			for rows.Next() {
				var id int64
				var nom, pre, pat, god, nmk sql.NullString
				_ = rows.Scan(&id, &nom, &pre, &pat, &god, &nmk)
				row := map[string]any{"patients_id": id}
				if nom.Valid {
					row["nom"] = nom.String
				}
				if pre.Valid {
					row["prenom"] = pre.String
				}
				if pat.Valid {
					row["patronym"] = pat.String
				}
				if god.Valid {
					row["god_rogdeniq"] = god.String
				}
				if nmk.Valid {
					row["nomer_mediciskoj_kart"] = nmk.String
				}
				found = append(found, row)
			}
			_ = rows.Close()
			out["patient_search"] = found
		}
		if arr, ok := out["patient_search"].([]map[string]any); ok && len(arr) > 0 {
			switch v := arr[0]["patients_id"].(type) {
			case int64:
				pid = v
			}
		}
	} else {
		out["patient_search"] = []any{map[string]any{"patients_id": pid, "note": "from BIRTHHUNT_PATIENTS_ID"}}
	}

	if pid == 0 {
		out["datetime_match_note"] = "no patient row; set BIRTHHUNT_LAST_NAME / BIRTHHUNT_FIRST_NAME or BIRTHHUNT_PATIENTS_ID"
	} else {
		rows, err := m.QueryContext(ctx, `
SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_NAME = 'PATIENTS'
  AND DATA_TYPE IN ('date','datetime','datetime2','smalldatetime')
ORDER BY ORDINAL_POSITION`)
		if err != nil {
			out["patients_datetime_probe_error"] = err.Error()
		} else {
			var hits []map[string]any
			for rows.Next() {
				var col string
				_ = rows.Scan(&col)
				qi := quoteIdent(col)
				q := fmt.Sprintf(
					`SELECT CAST(%s AS varchar(40)) AS val FROM PATIENTS WHERE PATIENTS_ID = @p AND CONVERT(date, %s) = @d`,
					qi, qi,
				)
				var val sql.NullString
				err := m.QueryRowContext(ctx, q,
					sql.Named("p", pid),
					sql.Named("d", dateStr),
				).Scan(&val)
				if err == nil && val.Valid {
					hits = append(hits, map[string]any{"column": col, "value": val.String})
				}
			}
			_ = rows.Close()
			out["patients_datetime_equals_date"] = hits
		}

		wrows, werr := m.QueryContext(ctx, `
SELECT TOP 200 t.TABLE_NAME, c.COLUMN_NAME, c.DATA_TYPE
FROM INFORMATION_SCHEMA.COLUMNS c
JOIN INFORMATION_SCHEMA.TABLES t ON t.TABLE_NAME = c.TABLE_NAME AND t.TABLE_SCHEMA = c.TABLE_SCHEMA
WHERE t.TABLE_TYPE = 'BASE TABLE'
  AND c.DATA_TYPE IN ('date','datetime','datetime2','smalldatetime')
  AND (t.TABLE_NAME LIKE '%PATIENT%' OR t.TABLE_NAME LIKE '%PAT_%' OR t.TABLE_NAME LIKE 'FM[_]%')
ORDER BY t.TABLE_NAME, c.COLUMN_NAME`)
		if werr != nil {
			out["wide_date_columns_error"] = werr.Error()
		} else {
			var wide []map[string]string
			for wrows.Next() {
				var tn, cn, dt string
				_ = wrows.Scan(&tn, &cn, &dt)
				wide = append(wide, map[string]string{"table_name": tn, "column_name": cn, "data_type": dt})
			}
			_ = wrows.Close()
			out["wide_fm_patient_date_columns_sample"] = wide
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func quoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}
