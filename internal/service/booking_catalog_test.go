package service

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func TestDateSpan_FourteenDays(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	days, err := slotDays("2026-11-06", "2026-11-19", now, 14)
	if err != nil || len(days) != 14 {
		t.Fatalf("14 days: %v %d", err, len(days))
	}
	if _, err := slotDays("2026-11-06", "2026-11-20", now, 30); err == nil {
		t.Fatal("15 days must be rejected even when BOOKING_MAX_DAYS is 30")
	}
}

func TestBookingSlots_OutsideHorizonDoesNotQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	svc := seededSlotService(db)
	svc.Config.BookingMaxDays = 30

	cases := []struct {
		name string
		from string
		to   string
		bad  bool
	}{
		{name: "year 2000", from: "2000-01-01", to: "2000-01-01"},
		{name: "today plus 40", from: "2026-12-16", to: "2026-12-16"},
		{name: "fifteen days", from: "2026-11-06", to: "2026-11-20", bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.BookingSlots(context.Background(), 1119, 10000688, tc.from, tc.to, now)
			if tc.bad {
				if err == nil {
					t.Fatal("15-day window must be rejected before SQL")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Fatalf("slots=%d", len(got))
			}
		})
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSlotCache_DoesNotGrowPastLimitOrKeepExpired(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	svc := seededSlotService(db)
	st := svc.booking()
	st.slots = map[string]slotCache{}
	for i := 0; i < slotCacheMax; i++ {
		st.slots[fmt.Sprintf("9:9:2099-01-%02d", i+1)] = slotCache{at: time.Now(), starts: nil}
	}
	st.slots["1119:10000688:1999-01-01"] = slotCache{at: time.Now().Add(-2 * time.Minute), starts: []string{"stale"}}

	mock.ExpectQuery("pl_TelemedGetAvaliableSlots").WillReturnRows(sqlmock.NewRows([]string{"D1"}).AddRow("2026-11-06 15:00:00"))
	starts, err := svc.daySlots(context.Background(), 1119, 10000688, "2026-11-06", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) != 1 {
		t.Fatalf("starts=%v", starts)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.slots) > slotCacheMax {
		t.Fatalf("cache size %d", len(st.slots))
	}
	if _, ok := st.slots["1119:10000688:1999-01-01"]; ok {
		t.Fatal("expired cache entry kept")
	}
	if _, ok := st.slots["1119:10000688:2026-11-06"]; ok {
		t.Fatal("cache grew past the limit")
	}
	outside, err := svc.daySlots(context.Background(), 1119, 10000688, "2000-01-01", now)
	if err != nil || outside != nil {
		t.Fatalf("outside day queried: %v %v", outside, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func seededSlotService(db *sql.DB) *Services {
	svc := &Services{
		MSSQL: db,
		Config: config.Config{
			SlotTokenSecret: "0123456789abcdef0123456789abcdef",
			BookingMaxDays:  14,
		},
		book: &bookingState{
			pairsAt: time.Now(),
			active: map[string]repo.BookingOption{
				pairKey(1119, 10000688): {PlSubjID: 1119, PlExamID: 10000688, DurationMin: 30, BranchID: 496},
			},
			slots: map[string]slotCache{},
		},
	}
	return svc
}
