package confirmations

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestApplyWaitlist_OutcomeKeepsOpenAndVersionBlocksTheLoser(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewRepo(db)
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	if _, err := r.Upsert(ctx, 10, 20, "declined", "max", "a", "v1", 106); err != nil {
		t.Fatal(err)
	}
	rows, _, err := r.ListWaitlist(ctx, WaitlistListFilter{Page: 1, PerPage: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	id := rows[0].ID
	row, err := r.ApplyWaitlist(ctx, id, 1, "no_answer", "", "a", now)
	if err != nil || row.Status != WaitlistStatusContacted || row.Outcome != "no_answer" || row.Version != 2 {
		t.Fatalf("%+v %v", row, err)
	}
	row, err = r.ApplyWaitlist(ctx, id, 2, "call_back", "", "b", now)
	if err != nil || row.Status != WaitlistStatusContacted || row.Outcome != "call_back" || row.Version != 3 {
		t.Fatalf("same status %+v %v", row, err)
	}
	var first, second error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, first = r.ApplyWaitlist(ctx, id, 3, "refused", "", "a", now)
	}()
	go func() {
		defer wg.Done()
		_, second = r.ApplyWaitlist(ctx, id, 3, "other", "", "b", now)
	}()
	wg.Wait()
	err = first
	if (err == nil) == (second == nil) {
		t.Fatalf("want one winner err=%v second=%v", err, second)
	}
	loser := err
	if loser == nil {
		loser = second
	}
	if !errors.Is(loser, ErrWaitlistChanged) {
		t.Fatal(loser)
	}
	fresh, err := r.GetWaitlist(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != WaitlistStatusClosed {
		t.Fatalf("status=%s", fresh.Status)
	}
	_, err = r.ApplyWaitlist(ctx, id, fresh.Version-1, "call_back", "", "a", now)
	if !errors.Is(err, ErrWaitlistChanged) {
		t.Fatalf("stale reopen %v", err)
	}
	again, err := r.GetWaitlist(ctx, id)
	if err != nil || again.Status != WaitlistStatusClosed {
		t.Fatalf("%+v %v", again, err)
	}
	opened, err := r.ApplyWaitlist(ctx, id, fresh.Version, "", "reopen", "a", now)
	if err != nil || opened.Status != WaitlistStatusContacted || opened.Outcome != "" {
		t.Fatalf("reopen %+v %v", opened, err)
	}
}

func TestApplyMisWork_FirstOutcomeAndReopen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewRepo(db)
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	row, err := r.ApplyMisWork(ctx, 501, 1, "no_answer", "", "reg", now)
	if err != nil || row.Status != WaitlistStatusContacted || row.Version != 2 {
		t.Fatalf("%+v %v", row, err)
	}
	row, err = r.ApplyMisWork(ctx, 501, 2, "call_back", "", "reg", now)
	if err != nil || row.Status != WaitlistStatusContacted || row.Outcome != "call_back" {
		t.Fatalf("%+v %v", row, err)
	}
	row, err = r.ApplyMisWork(ctx, 501, row.Version, "refused", "", "reg", now)
	if err != nil || row.Status != WaitlistStatusClosed {
		t.Fatalf("%+v %v", row, err)
	}
	opened, err := r.ApplyMisWork(ctx, 501, row.Version, "", "reopen", "reg", now)
	if err != nil || opened.Status != WaitlistStatusContacted || opened.Outcome != "" {
		t.Fatalf("reopen %+v %v", opened, err)
	}
}
