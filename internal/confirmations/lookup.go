package confirmations

import (
	"context"
	"fmt"
	"strings"
)

// StatusByPlanningIDs returns confirmation status per planning_id (batch, no N+1).
func (r *Repo) StatusByPlanningIDs(ctx context.Context, planningIDs []int64) (map[int64]string, error) {
	if len(planningIDs) == 0 {
		return map[int64]string{}, nil
	}

	var b strings.Builder
	b.WriteString("SELECT planning_id, status FROM appointment_confirmations WHERE planning_id IN (")
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

	out := make(map[int64]string, len(planningIDs))
	for rows.Next() {
		var id int64
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, fmt.Errorf("scan confirmations overlay: %w", err)
		}
		out[id] = status
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows confirmations overlay: %w", err)
	}
	return out, nil
}
