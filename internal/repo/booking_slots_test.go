package repo

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestKeepSlot_DropsPastAndNearAndFar(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, loc)
	if KeepSlot("2026-11-06T10:00:00", now, 2, 14) {
		t.Fatal("past slot kept")
	}
	if KeepSlot("2026-11-06T13:30:00", now, 2, 14) {
		t.Fatal("slot inside 2 hours kept")
	}
	if !KeepSlot("2026-11-06T14:00:00", now, 2, 14) {
		t.Fatal("slot at the 2 hour mark dropped")
	}
	if KeepSlot("2026-11-21T09:00:00", now, 2, 14) {
		t.Fatal("slot after today+14 kept")
	}
	if !KeepSlot("2026-11-20T23:30:00", now, 2, 14) {
		t.Fatal("slot on today+14 dropped")
	}
}

func TestKeepSlot_DoesNotShiftWallClock(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 6, 22, 0, 0, 0, loc)
	// 23:30 is 90 minutes ahead. A +3h shift would make it 02:30 and pass the 2h cutoff.
	if KeepSlot("2026-11-06T23:30:00", now, 2, 14) {
		t.Fatal("23:30 was treated as a later instant")
	}
	later := time.Date(2026, 11, 6, 20, 0, 0, 0, loc)
	if !KeepSlot("2026-11-06T23:30:00", later, 2, 14) {
		t.Fatal("23:30 wall clock dropped")
	}
}

func TestBookingSlotsSQL_ReadOnly(t *testing.T) {
	b, err := os.ReadFile("booking_slots.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"CONVERT(varchar(19), t.D1, 120)",
		"CONVERT(date, @from, 23)",
		"t.EType = 0",
		"pl_TelemedGetAvaliableSlots",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if SQLLooksMutating(src) {
		t.Fatal("slot SQL must be read-only")
	}
}
