package booking

import "testing"

func cr(id int64) *int64 { return &id }

func baseOne(planning int64) Admission {
	return Admission{
		Found: true, Active: true,
		Answers:           []Answer{{ID: 7, PatdirecID: 8, HasPatdirec: true, AnswState: 0, BillNull: true}},
		Directions:        []Direction{{ID: 8, PlanningCR: cr(planning)}},
		OwnDirection:      &Direction{ID: 8, PlanningCR: cr(planning)},
		OtherAnswersOnDir: 0,
	}
}

func TestAdmit(t *testing.T) {
	okNone := Admission{Found: true, Active: true}
	if v, ok := Admit(1, okNone); !ok || v != "none" {
		t.Fatalf("none %+v %v", v, ok)
	}
	withDir := baseOne(5)
	if v, ok := Admit(5, withDir); !ok || v != "one" {
		t.Fatalf("one %+v %v", v, ok)
	}
	dirNoAnswer := Admission{Directions: []Direction{{ID: 1, PlanningCR: cr(5)}}}
	if _, ok := Admit(5, dirNoAnswer); ok {
		t.Fatal("direction without answer must be refused")
	}
	two := baseOne(5)
	two.Directions = append(two.Directions, Direction{ID: 9, PlanningCR: cr(5)})
	if _, ok := Admit(5, two); ok {
		t.Fatal("two directions")
	}
	bill := baseOne(5)
	bill.Answers[0].BillNull = false
	if _, ok := Admit(5, bill); ok {
		t.Fatal("bill")
	}
	state := baseOne(5)
	state.Answers[0].AnswState = 1
	if _, ok := Admit(5, state); ok {
		t.Fatal("answ state")
	}
	nullCR := baseOne(5)
	nullCR.OwnDirection = &Direction{ID: 8, PlanningCR: nil}
	nullCR.Directions[0].PlanningCR = nil
	if _, ok := Admit(5, nullCR); ok {
		t.Fatal("null cr")
	}
	other := baseOne(5)
	other.OwnDirection = &Direction{ID: 8, PlanningCR: cr(99)}
	other.Directions = []Direction{{ID: 8, PlanningCR: cr(99)}}
	if _, ok := Admit(5, other); ok {
		t.Fatal("foreign cr")
	}
	docs := baseOne(5)
	docs.VerifiedDocs = 1
	if _, ok := Admit(5, docs); ok {
		t.Fatal("verified doc")
	}
	noPat := baseOne(5)
	noPat.Answers[0].HasPatdirec = false
	noPat.OwnDirection = nil
	if _, ok := Admit(5, noPat); ok {
		t.Fatal("answer without direction")
	}
}

func snapSet(a, p []int64, cancelled int) Snap {
	return Snap{
		ReadOK: true, PlanningFound: true, PlanningCancelled: cancelled,
		AnswExpected: false, DirExpected: false, A: a, P: p,
	}
}

func TestCancelPredicates(t *testing.T) {
	before := snapSet(nil, nil, 0)
	full := snapSet(nil, nil, 1)
	pred := Eval(1, "none", before, full)
	if !pred.Full || pred.External {
		t.Fatalf("full none: %+v", pred)
	}
	st, code := CancelOutcome(true, pred, true)
	if st != StateOK || code != "" {
		t.Fatalf("finished full: %s %s", st, code)
	}
	st, code = CancelOutcome(false, pred, true)
	if st != StateOK {
		t.Fatalf("unfinished full: %s", st)
	}

	same := snapSet(nil, nil, 0)
	unchanged := Eval(1, "none", before, same)
	if !unchanged.Unchanged || unchanged.Full {
		t.Fatalf("unchanged: %+v", unchanged)
	}
	st, code = CancelOutcome(true, unchanged, true)
	if st != StateRejected || code != "CANCEL_FAILED" {
		t.Fatalf("finished unchanged: %s %s", st, code)
	}
	st, _ = CancelOutcome(false, unchanged, true)
	if st != StateUnknown {
		t.Fatalf("unfinished unchanged: %s", st)
	}

	beforeOne := snapSet([]int64{7}, []int64{8}, 0)
	beforeOne.AnswFound = true
	beforeOne.AnswPlanning = cr(1)
	beforeOne.DirFound = true
	partialAfter := snapSet([]int64{7}, []int64{8}, 0)
	partialAfter.AnswFound = true
	partialAfter.AnswState = 2
	partialAfter.AnswPlanning = nil
	partialAfter.DirFound = true
	part := Eval(1, "one", beforeOne, partialAfter)
	if part.Full || part.Unchanged {
		t.Fatalf("partial: %+v", part)
	}
	st, code = CancelOutcome(true, part, true)
	if st != StatePartial || code != "CANCEL_PARTIAL" {
		t.Fatalf("finished partial: %s %s", st, code)
	}
	st, _ = CancelOutcome(false, part, true)
	if st != StateUnknown {
		t.Fatal("unfinished partial must stay unknown")
	}

	replaced := snapSet([]int64{2}, []int64{3}, 1)
	ext := Eval(1, "none", snapSet([]int64{1}, []int64{1}, 0), replaced)
	if !ext.External || ext.Full {
		t.Fatalf("replaced ids: %+v", ext)
	}
	st, code = CancelOutcome(true, ext, true)
	if st != StatePartial || code != "EXTERNAL_CHANGE" {
		t.Fatalf("external: %s %s", st, code)
	}

	bill := snapSet([]int64{7}, []int64{8}, 1)
	bill.AnswFound = true
	bill.AnswBill = cr(99)
	bill.AnswState = 2
	bill.AnswPlanning = cr(1)
	bill.DirFound = true
	bill.DirCancelled = 1
	bill.DirState = 3
	if !Eval(1, "one", beforeOne, bill).External {
		t.Fatal("bill must be external")
	}

	doc := snapSet(nil, nil, 1)
	doc.D = []int64{4}
	if !Eval(1, "none", before, doc).External {
		t.Fatal("verified doc")
	}

	deleted := snapSet(nil, nil, 1)
	beforeSaved := snapSet([]int64{7}, []int64{8}, 0)
	beforeSaved.AnswFound = true
	beforeSaved.AnswPlanning = cr(1)
	beforeSaved.DirFound = true
	if !Eval(1, "one", beforeSaved, deleted).Full {
		t.Fatal("deleted saved rows are a full cancel when planning is cancelled")
	}

	related := snapSet(nil, nil, 0)
	if Eval(1, "one", beforeSaved, related).Full {
		t.Fatal("planning still active is not a full cancel")
	}

	unlinked := snapSet([]int64{7}, []int64{8}, 1)
	unlinked.AnswFound = true
	unlinked.AnswState = 0
	unlinked.AnswPlanning = nil
	unlinked.DirFound = true
	if pred := Eval(1, "one", beforeSaved, unlinked); pred.Unchanged || pred.Full {
		t.Fatalf("unlinked answer: %+v", pred)
	}

	_, _ = CancelOutcome(true, Predicates{}, false)
	if st, code := CancelOutcome(true, Predicates{}, false); st != StateUnknown || code != "CANCEL_UNKNOWN" {
		t.Fatalf("snap fail: %s %s", st, code)
	}
}
