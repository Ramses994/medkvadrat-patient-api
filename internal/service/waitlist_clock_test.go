package service

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is the test dashboard clock. Now and Advance share an atomic
// instant, so a background sweep can read the clock while a test moves it.
type fakeClock struct {
	ns atomic.Int64
}

func newFakeClock(t time.Time) *fakeClock {
	c := &fakeClock{}
	c.Set(t)
	return c
}

func (c *fakeClock) Now() time.Time {
	return time.Unix(0, c.ns.Load()).UTC()
}

func (c *fakeClock) Advance(d time.Duration) {
	c.ns.Add(int64(d))
}

func (c *fakeClock) Set(t time.Time) {
	c.ns.Store(t.UnixNano())
}

// setMisNow replaces the dashboard clock until the test ends.
// The previous clock is restored in t.Cleanup, so a background sweep
// never races a plain assignment of the function value.
func setMisNow(t *testing.T, fn func() time.Time) {
	t.Helper()
	if fn == nil {
		t.Fatal("nil clock")
	}
	prev := misNowPtr.Load()
	misNowPtr.Store(&fn)
	t.Cleanup(func() { misNowPtr.Store(prev) })
}

func TestFakeClock_AdvanceWhileSweepReads(t *testing.T) {
	startSnapshotSweep()
	clock := newFakeClock(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC))
	setMisNow(t, clock.Now)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = misNow()
			}
		}
	}()
	for i := 0; i < 200; i++ {
		clock.Advance(time.Millisecond)
		_ = misNow()
	}
	close(stop)
	wg.Wait()
	if got := clock.Now(); !got.Equal(time.Date(2026, 11, 6, 12, 0, 0, int(200*time.Millisecond), time.UTC)) {
		t.Fatalf("clock=%s", got)
	}
}
