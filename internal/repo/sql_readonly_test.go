package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Read-path SQL must not mutate Medialog. The only exception is the named RW batches.
func TestReadRepoSQL_NoExecOrDbFunctions(t *testing.T) {
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if filepath.Base(path) == "booking_rw.go" {
			var ok bool
			src, ok = stripNamedRWBatches(src)
			if !ok {
				t.Errorf("%s: approved RW batches were not found by name", path)
				return nil
			}
		}
		if SQLLooksMutating(src) {
			t.Errorf("%s: read repo must not contain mutating SQL", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadRepoSQL_NewSQLInBookingRWIsChecked(t *testing.T) {
	b, err := os.ReadFile("booking_rw.go")
	if err != nil {
		t.Fatal(err)
	}
	src, ok := stripNamedRWBatches(string(b))
	if !ok {
		t.Fatal("named RW batches missing")
	}
	if SQLLooksMutating(src) {
		t.Fatal("approved batches were not the only mutating SQL")
	}
	if !SQLLooksMutating(src + "\nconst extraSQL = `EXEC dbo.evil`") {
		t.Fatal("new SQL in booking_rw.go was ignored")
	}
}

func stripNamedRWBatches(src string) (string, bool) {
	for _, name := range []string{"addVisitSQL", "cancelVisitSQL"} {
		next, ok := stripNamedConst(src, name)
		if !ok {
			return src, false
		}
		src = next
	}
	return src, true
}

func stripNamedConst(src, name string) (string, bool) {
	key := name + " = `"
	i := strings.Index(src, key)
	if i < 0 {
		return src, false
	}
	rest := src[i+len(key):]
	j := strings.Index(rest, "`")
	if j < 0 {
		return src, false
	}
	return src[:i+len(key)] + rest[j:], true
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
