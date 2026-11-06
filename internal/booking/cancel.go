package booking

// Answer is one DIR_ANSW row tied to the booking by PLANNING_ID.
type Answer struct {
	ID          int64
	PatdirecID  int64
	HasPatdirec bool
	AnswState   int
	BillNull    bool
}

// Direction is one PATDIREC row.
type Direction struct {
	ID         int64
	Cancelled  int
	DirState   int
	PlanningCR *int64
}

// Admission is the read model used before a cancel call.
type Admission struct {
	Found             bool
	PatientID         int64
	PlSubjID          int64
	OrgID             int
	DateStart         string
	Active            bool
	Answers           []Answer
	Directions        []Direction
	OwnDirection      *Direction
	OtherAnswersOnDir int
	VerifiedDocs      int
}

// Admit reports whether the pilot may cancel. variant is "none" or "one".
func Admit(planningID int64, in Admission) (variant string, ok bool) {
	if in.VerifiedDocs > 0 {
		return "", false
	}
	if len(in.Answers) == 0 && len(in.Directions) == 0 {
		return "none", true
	}
	if len(in.Answers) != 1 || len(in.Directions) != 1 {
		return "", false
	}
	ans := in.Answers[0]
	if ans.AnswState != 0 || !ans.BillNull || !ans.HasPatdirec || in.OwnDirection == nil {
		return "", false
	}
	dir := in.OwnDirection
	if dir.ID != ans.PatdirecID || dir.ID != in.Directions[0].ID {
		return "", false
	}
	if dir.PlanningCR == nil || *dir.PlanningCR != planningID || dir.Cancelled != 0 {
		return "", false
	}
	if in.OtherAnswersOnDir != 0 {
		return "", false
	}
	return "one", true
}

// Snap is the before/after picture addressed by saved ids, plus the id sets.
type Snap struct {
	ReadOK            bool
	PlanningFound     bool
	PlanningCancelled int
	PlanningStatus    int
	AnswExpected      bool
	AnswFound         bool
	AnswState         int
	AnswPlanning      *int64
	AnswBill          *int64
	DirExpected       bool
	DirFound          bool
	DirCancelled      int
	DirState          int
	A                 []int64
	P                 []int64
	D                 []int64
}

func hasNew(after, before []int64) bool {
	seen := map[int64]struct{}{}
	for _, id := range before {
		seen[id] = struct{}{}
	}
	for _, id := range after {
		if _, ok := seen[id]; !ok {
			return true
		}
	}
	return false
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int64]int{}
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		seen[id]--
		if seen[id] < 0 {
			return false
		}
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// SnapAllows is the last check before pl_CancelVisit. The code is empty when the call may proceed.
func SnapAllows(first, last Snap) (code string, ok bool) {
	if !last.ReadOK {
		return "CANCEL_NOT_SUPPORTED", false
	}
	if last.PlanningCancelled != 0 {
		return "ALREADY_CANCELLED", false
	}
	if !last.PlanningFound || last.PlanningStatus != 0 || last.AnswBill != nil {
		return "CANCEL_NOT_SUPPORTED", false
	}
	if !sameIDs(first.A, last.A) || !sameIDs(first.P, last.P) || !sameIDs(first.D, last.D) {
		return "CANCEL_NOT_SUPPORTED", false
	}
	return "", true
}

func samePtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Predicates are shared by the handler, the background reconcile and manual resolve.
type Predicates struct {
	External  bool
	Full      bool
	Unchanged bool
}

func Eval(planningID int64, variant string, before, after Snap) Predicates {
	external := hasNew(after.A, before.A) || hasNew(after.P, before.P) || len(after.D) > 0 || (after.AnswFound && after.AnswBill != nil)
	ansDone := !after.AnswFound || (after.AnswState == 2 && after.AnswPlanning != nil && *after.AnswPlanning == planningID)
	dirDone := !after.DirFound || (after.DirCancelled == 1 && after.DirState == 3)
	noDir := variant == "none" && len(after.A) == 0 && len(after.P) == 0
	withDir := variant == "one" && ansDone && dirDone
	full := after.PlanningCancelled == 1 && !external && (noDir || withDir)
	fields := after.PlanningCancelled == before.PlanningCancelled &&
		after.PlanningStatus == before.PlanningStatus &&
		after.AnswFound == before.AnswFound &&
		after.AnswState == before.AnswState &&
		samePtr(after.AnswPlanning, before.AnswPlanning) &&
		samePtr(after.AnswBill, before.AnswBill) &&
		after.DirFound == before.DirFound &&
		after.DirCancelled == before.DirCancelled &&
		after.DirState == before.DirState
	unchanged := !external && sameIDs(after.A, before.A) && sameIDs(after.P, before.P) && fields
	return Predicates{External: external, Full: full, Unchanged: unchanged}
}

// CancelOutcome maps a finished or unfinished call to a journal state and code.
func CancelOutcome(callFinished bool, pred Predicates, snapOK bool) (state, code string) {
	if !snapOK {
		return StateUnknown, "CANCEL_UNKNOWN"
	}
	if pred.External {
		if callFinished {
			return StatePartial, "EXTERNAL_CHANGE"
		}
		return StateUnknown, "CANCEL_UNKNOWN"
	}
	if pred.Full {
		return StateOK, ""
	}
	if pred.Unchanged {
		if callFinished {
			return StateRejected, "CANCEL_FAILED"
		}
		return StateUnknown, "CANCEL_UNKNOWN"
	}
	if callFinished {
		return StatePartial, "CANCEL_PARTIAL"
	}
	return StateUnknown, "CANCEL_UNKNOWN"
}
