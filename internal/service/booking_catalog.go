package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/slottoken"
)

const (
	pairCacheTTL = 10 * time.Minute
	slotCacheTTL = 60 * time.Second
)

type bookingState struct {
	mu      sync.Mutex
	pairsAt time.Time
	active  map[string]repo.BookingOption
	slots   map[string]slotCache
}

type slotCache struct {
	at     time.Time
	starts []string
}

func (s *Services) booking() *bookingState {
	if s.book == nil {
		s.book = &bookingState{active: map[string]repo.BookingOption{}, slots: map[string]slotCache{}}
	}
	return s.book
}

func pairKey(subj, exam int64) string {
	return fmt.Sprintf("%d:%d", subj, exam)
}

// RefreshBookingPairs reloads the pilot pairs. Inactive ones are logged by id and reason.
func (s *Services) RefreshBookingPairs(ctx context.Context) {
	if s.MSSQL == nil {
		return
	}
	active := map[string]repo.BookingOption{}
	for _, p := range s.Config.BookingPairs {
		opt, err := repo.LoadBookingPair(ctx, s.MSSQL, p.PlSubjID, p.PlExamID)
		if err != nil {
			var inactive repo.InactivePairError
			if errors.As(err, &inactive) {
				s.log().Warn("booking pair inactive", "pl_subj_id", p.PlSubjID, "pl_exam_id", p.PlExamID, "reason", inactive.Reason)
				continue
			}
			s.log().Error("booking pair load failed", "pl_subj_id", p.PlSubjID, "pl_exam_id", p.PlExamID, "err", err)
			continue
		}
		active[pairKey(p.PlSubjID, p.PlExamID)] = opt
	}
	st := s.booking()
	st.mu.Lock()
	st.active = active
	st.pairsAt = time.Now()
	st.mu.Unlock()
}

func (s *Services) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Services) ensurePairs(ctx context.Context) {
	st := s.booking()
	st.mu.Lock()
	fresh := !st.pairsAt.IsZero() && time.Since(st.pairsAt) < pairCacheTTL
	st.mu.Unlock()
	if fresh {
		return
	}
	s.RefreshBookingPairs(ctx)
}

func (s *Services) activePair(ctx context.Context, subj, exam int64) (repo.BookingOption, bool) {
	s.ensurePairs(ctx)
	st := s.booking()
	st.mu.Lock()
	defer st.mu.Unlock()
	opt, ok := st.active[pairKey(subj, exam)]
	return opt, ok
}

// BookingOptions lists active pilot pairs.
func (s *Services) BookingOptions(ctx context.Context) ([]repo.BookingOption, error) {
	s.ensurePairs(ctx)
	st := s.booking()
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]repo.BookingOption, 0, len(st.active))
	for _, opt := range st.active {
		out = append(out, opt)
	}
	return out, nil
}

// BookingSlots returns offers inside the window for one active pair.
func (s *Services) BookingSlots(ctx context.Context, subj, exam int64, from, to string, now time.Time) ([]repo.BookingSlot, error) {
	if !slottoken.SecretOK(s.Config.SlotTokenSecret) {
		s.log().Error("slot token secret not configured")
		return nil, slottoken.ErrSecret
	}
	days, err := dateSpan(from, to, s.Config.BookingMaxDays)
	if err != nil {
		return nil, repo.ErrBadSlotWindow
	}
	opt, ok := s.activePair(ctx, subj, exam)
	if !ok {
		return nil, repo.ErrInactivePair
	}
	offers := make([]repo.BookingSlot, 0)
	for _, day := range days {
		starts, err := s.daySlots(ctx, subj, exam, day)
		if err != nil {
			s.log().Error("slots unavailable", "pl_subj_id", subj, "pl_exam_id", exam)
			return nil, repo.ErrSlotsUnavailable
		}
		for _, start := range starts {
			if !repo.KeepSlot(start, now, s.Config.BookingMinHoursAhead, s.Config.BookingMaxDays) {
				continue
			}
			offers = append(offers, repo.BookingSlot{
				Start: start, DurationMin: opt.DurationMin, BranchID: opt.BranchID,
				PlSubjID: subj, PlExamID: exam,
			})
		}
	}
	return offers, nil
}

// InvalidateSlotCache drops cached slots for one line after a book or cancel.
func (s *Services) InvalidateSlotCache(plSubjID int64) {
	prefix := fmt.Sprintf("%d:", plSubjID)
	st := s.booking()
	st.mu.Lock()
	defer st.mu.Unlock()
	for k := range st.slots {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(st.slots, k)
		}
	}
}

func (s *Services) daySlots(ctx context.Context, subj, exam int64, day string) ([]string, error) {
	key := fmt.Sprintf("%d:%d:%s", subj, exam, day)
	st := s.booking()
	st.mu.Lock()
	if hit, ok := st.slots[key]; ok && time.Since(hit.at) < slotCacheTTL {
		out := append([]string(nil), hit.starts...)
		st.mu.Unlock()
		return out, nil
	}
	st.mu.Unlock()
	starts, err := repo.LoadDaySlots(ctx, s.MSSQL, subj, exam, day)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	if st.slots == nil {
		st.slots = map[string]slotCache{}
	}
	st.slots[key] = slotCache{at: time.Now(), starts: starts}
	st.mu.Unlock()
	return starts, nil
}

func dateSpan(from, to string, maxDays int) ([]string, error) {
	a, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, err
	}
	b, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, err
	}
	if b.Before(a) {
		return nil, fmt.Errorf("order")
	}
	n := int(b.Sub(a).Hours()/24) + 1
	if n > maxDays || n <= 0 {
		return nil, fmt.Errorf("span")
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, a.AddDate(0, 0, i).Format("2006-01-02"))
	}
	return out, nil
}

// SlotSecretConfigured reports whether slots and booking can sign tokens.
func SlotSecretConfigured(cfg config.Config) bool {
	return slottoken.SecretOK(cfg.SlotTokenSecret)
}
