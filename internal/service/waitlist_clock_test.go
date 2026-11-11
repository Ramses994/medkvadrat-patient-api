package service

import (
	"testing"
	"time"
)

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
