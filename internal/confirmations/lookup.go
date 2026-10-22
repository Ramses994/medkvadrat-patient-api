package confirmations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type Overlay struct {
	Status  string
	Version string
}

// OverlayByPlanningIDs returns confirmation status and appt_version per planning_id.
func (r *Repo) OverlayByPlanningIDs(ctx context.Context, planningIDs []int64) (map[int64]Overlay, error) {
	if len(planningIDs) == 0 {
		return map[int64]Overlay{}, nil
	}

	var b strings.Builder
	b.WriteString("SELECT planning_id, status, appt_version FROM appointment_confirmations WHERE planning_id IN (")
	args := make([]any, len(planningIDs))
	for i, id := range planningIDs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("?")
		args[i] = id
	}
	b.WriteString(")")

	rows, err := r.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query confirmations overlay: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]Overlay, len(planningIDs))
	for rows.Next() {
		var id int64
		var status string
		var ver sql.NullString
		if err := rows.Scan(&id, &status, &ver); err != nil {
			return nil, fmt.Errorf("scan confirmations overlay: %w", err)
		}
		o := Overlay{Status: status}
		if ver.Valid {
			o.Version = ver.String
		}
		out[id] = o
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows confirmations overlay: %w", err)
	}
	return out, nil
}

// StatusByPlanningIDs returns confirmation status per planning_id (batch, no N+1).
func (r *Repo) StatusByPlanningIDs(ctx context.Context, planningIDs []int64) (map[int64]string, error) {
	o, err := r.OverlayByPlanningIDs(ctx, planningIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(o))
	for id, v := range o {
		out[id] = v.Status
	}
	return out, nil
}
