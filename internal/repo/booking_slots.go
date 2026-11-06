package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInactivePair     = errors.New("inactive pair")
	ErrSlotsUnavailable = errors.New("slots unavailable")
	ErrBadSlotWindow    = errors.New("bad slot window")
)

// BookingSlot is one free start offered to the patient.
type BookingSlot struct {
	Start       string
	DurationMin int
	BranchID    int
	PlSubjID    int64
	PlExamID    int64
}

// SlotStartLayout is the patient-facing wall clock, Moscow digits, no offset.
const SlotStartLayout = "2006-01-02T15:04:05"

// LoadDaySlots calls pl_TelemedGetAvaliableSlots for one calendar day and returns EType=0 starts.
// The day bounds are date strings. D1 is returned as text so the clock is not shifted.
func LoadDaySlots(ctx context.Context, db *sql.DB, plSubjID, plExamID int64, day string) ([]string, error) {
	next, err := nextDate(day)
	if err != nil {
		return nil, err
	}
	const q = `
SELECT CONVERT(varchar(19), t.D1, 120)
FROM dbo.pl_TelemedGetAvaliableSlots(@subj, CONVERT(date, @from, 23), CONVERT(date, @to, 23), @exam) t
WHERE t.EType = 0`
	rows, err := db.QueryContext(ctx, q,
		sql.Named("subj", plSubjID),
		sql.Named("from", day),
		sql.Named("to", next),
		sql.Named("exam", plExamID),
	)
	if err != nil {
		return nil, fmt.Errorf("query slots: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan slot: %w", err)
		}
		wall := strings.TrimSpace(raw)
		if wall == "" {
			continue
		}
		wall = strings.Replace(wall, " ", "T", 1)
		out = append(out, wall)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nextDate(day string) (string, error) {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("day: %w", err)
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02"), nil
}

// KeepSlot drops starts before now+minHours and after today+maxDays. Both clocks are Moscow wall time.
func KeepSlot(start string, now time.Time, minHours, maxDays int) bool {
	loc := moscowLocation()
	at, err := time.ParseInLocation(SlotStartLayout, start, loc)
	if err != nil {
		return false
	}
	nowM := now.In(loc)
	if at.Before(nowM.Add(time.Duration(minHours) * time.Hour)) {
		return false
	}
	today := time.Date(nowM.Year(), nowM.Month(), nowM.Day(), 0, 0, 0, 0, loc)
	limit := today.AddDate(0, 0, maxDays)
	startDay := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
	return !startDay.After(limit)
}
