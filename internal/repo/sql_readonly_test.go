package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Read-path SQL must not mutate Medialog. The only exception is the RW batch in booking_rw.go.
func TestReadRepoSQL_NoExecOrDbFunctions(t *testing.T) {
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(path) == "booking_rw.go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if SQLLooksMutating(string(b)) {
			t.Errorf("%s: read repo must not contain mutating SQL", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLLooksMutating_CatchesSplitExec(t *testing.T) {
	src := "set x=1\nexec\n dbo.pl_AddVisitContext\nEXECUTE dbo.pl_CancelVisit"
	if !SQLLooksMutating(src) {
		t.Fatal("guard missed exec across a newline and EXECUTE")
	}
	if SQLLooksMutating("SELECT CONVERT(varchar(19), t.D1, 120) FROM dbo.pl_TelemedGetAvaliableSlots(@subj, @from, @to, @exam)") {
		t.Fatal("slot read was treated as a write")
	}
}
