package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Read-path SQL must not call stored procedures or dbo functions (RO login has DENY EXECUTE).
func TestReadRepoSQL_NoExecOrDbFunctions(t *testing.T) {
	exclude := map[string]bool{
		"book.go": true, "booking_me.go": true, "planning.go": true,
	}
	reExec := regexp.MustCompile(`(?i)\bEXEC\b`)
	reFn := regexp.MustCompile(`(?i)\bdbo\.(fn|Get)`)

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		base := filepath.Base(path)
		if exclude[base] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if reExec.MatchString(src) || reFn.MatchString(src) {
			t.Errorf("%s: read repo must not contain EXEC or dbo.fn/Get", path)
		}
		if !exclude[base] && SQLLooksMutating(src) && (base == "reminders.go" || base == "new_bookings.go" || base == "dashboard_schedule.go" || base == "planning_owner.go" || base == "waitlist_medialog.go") {
			t.Errorf("%s: notify/dashboard SQL must not contain INSERT|UPDATE|DELETE|MERGE|EXEC", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
