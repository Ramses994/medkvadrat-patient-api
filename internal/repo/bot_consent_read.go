package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MessengerNotifyByPatient reads DATA_W3_RASSYLKA.MESSENGER_NOTIFY for one patient.
// No row and a NULL flag are both nil. The statement is read-only.
func MessengerNotifyByPatient(ctx context.Context, db *sql.DB, patientID int64) (*int, error) {
	if db == nil || patientID <= 0 {
		return nil, nil
	}
	var n nullBitInt
	err := db.QueryRowContext(ctx, `
SELECT CAST(r.MESSENGER_NOTIFY AS int) AS MESSENGER_NOTIFY
FROM DATA_W3_RASSYLKA r
WHERE r.PATIENTS_ID = @patient_id`, sql.Named("patient_id", patientID)).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) || !n.Valid {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("messenger notify: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("messenger notify: %w", err)
	}
	v := n.Int
	return &v, nil
}

// PatientNamesByID reads ФИО for the ads-consent export. Read-only.
func PatientNamesByID(ctx context.Context, db *sql.DB, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if db == nil || len(ids) == 0 {
		return out, nil
	}
	const chunk = 400
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		part, err := patientNamesChunk(ctx, db, ids[start:end])
		if err != nil {
			return nil, err
		}
		for id, name := range part {
			out[id] = name
		}
	}
	return out, nil
}

func patientNamesChunk(ctx context.Context, db *sql.DB, ids []int64) (map[int64]string, error) {
	holders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if id <= 0 {
			continue
		}
		name := fmt.Sprintf("pid%d", i)
		holders = append(holders, "@"+name)
		args = append(args, sql.Named(name, id))
	}
	if len(holders) == 0 {
		return map[int64]string{}, nil
	}
	q := `
SELECT PATIENTS_ID,
  LTRIM(RTRIM(ISNULL(NOM,'') + ' ' + ISNULL(PRENOM,'') + ' ' + ISNULL(PATRONYME,''))) AS FULL_NAME
FROM PATIENTS
WHERE PATIENTS_ID IN (` + strings.Join(holders, ",") + `)`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("patient names: %w", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = strings.TrimSpace(name)
	}
	return out, rows.Err()
}
