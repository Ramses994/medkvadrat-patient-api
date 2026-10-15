package medialogwrite

import "testing"

func TestGuard_Checks(t *testing.T) {
	g := NewGuard(false, true, []int64{1}, []int64{10})
	if d := g.CheckEnabled(); d == nil || d.Code != "WRITE_DISABLED" {
		t.Fatalf("enabled: %+v", d)
	}

	g = NewGuard(true, false, []int64{1}, []int64{10})
	if d := g.CheckRWConfigured(); d == nil || d.Code != "WRITE_NOT_CONFIGURED" {
		t.Fatalf("rw: %+v", d)
	}

	g = NewGuard(true, true, nil, []int64{10})
	if d := g.CheckPatient(1); d == nil || d.Decision != DecisionDeniedPatient {
		t.Fatalf("empty patients: %+v", d)
	}

	g = NewGuard(true, true, []int64{1}, nil)
	if d := g.CheckPlSubj(10); d == nil || d.Decision != DecisionDeniedPlSubj {
		t.Fatalf("empty pl_subj: %+v", d)
	}

	g = NewGuard(true, true, []int64{1}, []int64{10})
	if d := g.CheckPatient(2); d == nil {
		t.Fatal("patient not allowed")
	}
	if d := g.CheckPlSubj(99); d == nil {
		t.Fatal("pl_subj not allowed")
	}
	if d := g.CheckPatient(1); d != nil {
		t.Fatalf("patient ok: %+v", d)
	}
	if d := g.CheckPlSubj(10); d != nil {
		t.Fatalf("pl_subj ok: %+v", d)
	}
}
