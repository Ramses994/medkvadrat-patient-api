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
	PlanningPatientID int64 // PLANNING.PATIENTS_ID, read in the same snapshot
	PlanningPlSubjID  int64 // PLANNING.PL_SUBJ_ID, read in the same snapshot
	AnswExpected      bool
	AnswFound         bool
	AnswState         int
	AnswPlanning      *int64
	AnswBill          *int64
	AnswPatdirec      *int64 // actual DIR_ANSW.PATDIREC_ID of the saved answer
	DirExpected       bool
	DirFound          bool
	DirCancelled      int
	DirState          int
	DirPlanningCR     *int64 // actual PATDIREC.PLANNING_CR_ID of the saved direction
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

// admissionFromSnap rebuilds the admit input from a snapshot so the last read
// uses the same Admit function as the first.
func admissionFromSnap(planningID int64, s Snap) Admission {
	adm := Admission{Found: s.PlanningFound, Active: s.PlanningFound && s.PlanningCancelled == 0 && s.PlanningStatus == 0}
	if len(s.D) > 0 {
		adm.VerifiedDocs = len(s.D)
	}
	hasAnswer := s.AnswFound || s.AnswBill != nil || s.AnswExpected || s.AnswState != 0 || len(s.A) > 0
	hasDir := s.DirFound || s.DirExpected || len(s.P) > 0
	if !hasAnswer && !hasDir {
		return adm
	}
	// Only fields actually read from DIR_ANSW / PATDIREC are used: the answer's
	// PATDIREC_ID and the direction's PLANNING_CR_ID are never inferred from ids,
	// so a re-linked answer or a re-owned direction fails Admit.
	billNull := s.AnswBill == nil
	dirID := int64(0)
	if len(s.P) > 0 {
		dirID = s.P[0]
	}
	if hasAnswer {
		n := len(s.A)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			id := int64(0)
			if i < len(s.A) {
				id = s.A[i]
			}
			a := Answer{ID: id, AnswState: s.AnswState, BillNull: billNull}
			if s.AnswPatdirec != nil {
				a.PatdirecID = *s.AnswPatdirec
				a.HasPatdirec = true
			}
			adm.Answers = append(adm.Answers, a)
		}
	}
	if hasDir {
		ids := s.P
		if len(ids) == 0 {
			ids = []int64{dirID}
		}
		for _, id := range ids {
			d := Direction{ID: id, Cancelled: s.DirCancelled, DirState: s.DirState}
			if s.DirPlanningCR != nil {
				v := *s.DirPlanningCR
				d.PlanningCR = &v
			}
			adm.Directions = append(adm.Directions, d)
		}
		if len(adm.Directions) == 1 {
			d := adm.Directions[0]
			adm.OwnDirection = &d
		}
	}
	return adm
}

// SnapAllows is the last check before pl_CancelVisit. The code is empty when the call may proceed.
// The last snapshot is admitted by the same Admit function as the first read.
func SnapAllows(planningID int64, firstVariant string, first, last Snap) (code string, ok bool) {
	if !last.ReadOK {
		return "CANCEL_NOT_SUPPORTED", false
	}
	if last.PlanningCancelled != 0 {
		return "ALREADY_CANCELLED", false
	}
	if !last.PlanningFound || last.PlanningStatus != 0 {
		return "CANCEL_NOT_SUPPORTED", false
	}
	variant, admitted := Admit(planningID, admissionFromSnap(planningID, last))
	if !admitted || variant != firstVariant {
		return "CANCEL_NOT_SUPPORTED", false
	}
	if !sameIDs(first.A, last.A) || !sameIDs(first.P, last.P) || !sameIDs(first.D, last.D) {
		return "CANCEL_NOT_SUPPORTED", false
	}
	if !samePtr(first.AnswPatdirec, last.AnswPatdirec) || !samePtr(first.DirPlanningCR, last.DirPlanningCR) ||
		!samePtr(first.AnswPlanning, last.AnswPlanning) {
		return "CANCEL_NOT_SUPPORTED", false
	}
	if first.PlanningPatientID != last.PlanningPatientID || first.PlanningPlSubjID != last.PlanningPlSubjID {
		return "CANCEL_NOT_SUPPORTED", false
	}
	if firstVariant == "one" {
		// The saved answer must still belong to this visit and point at the saved
		// direction, which this visit created (all read, never inferred).
		if !last.AnswFound || last.AnswPlanning == nil || *last.AnswPlanning != planningID {
			return "CANCEL_NOT_SUPPORTED", false
		}
		if !last.DirFound || last.DirPlanningCR == nil || *last.DirPlanningCR != planningID {
			return "CANCEL_NOT_SUPPORTED", false
		}
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

// relinked reports a link of a row that still exists pointing somewhere else than
// before. pl_CancelVisit never changes these links for a visit-owned direction
// (S18, R4: ANSW_STATE=2 and PATDIREC CANCELLED=1/DIR_STATE=3 only); a deleted row
// (the client's path) is not a re-link.
func relinked(planningID int64, before, after Snap) bool {
	// The visit itself moved to another patient or line.
	if before.PlanningFound && after.PlanningFound &&
		(before.PlanningPatientID != after.PlanningPatientID || before.PlanningPlSubjID != after.PlanningPlSubjID) {
		return true
	}
	if before.AnswFound && after.AnswFound {
		if !samePtr(before.AnswPatdirec, after.AnswPatdirec) {
			return true
		}
		if after.AnswPlanning != nil && *after.AnswPlanning != planningID {
			return true
		}
	}
	if before.DirFound && after.DirFound && !samePtr(before.DirPlanningCR, after.DirPlanningCR) {
		return true
	}
	return false
}

func Eval(planningID int64, variant string, before, after Snap) Predicates {
	external := hasNew(after.A, before.A) || hasNew(after.P, before.P) || len(after.D) > 0 || (after.AnswFound && after.AnswBill != nil) ||
		relinked(planningID, before, after)
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
		samePtr(after.AnswPatdirec, before.AnswPatdirec) &&
		samePtr(after.DirPlanningCR, before.DirPlanningCR) &&
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
