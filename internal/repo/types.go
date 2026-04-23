package repo

import "time"

type ScheduleChange struct {
	MotconsuID  int
	PatientID   int
	PatientName string
	DoctorID    int
	DoctorName  string
	DateConsult time.Time
	ModifyDate  time.Time
}

type FreeSlot struct {
	PlanningID int
	DoctorName string
	PlSubjID   int
	DateCons   time.Time
	Heure      int
	Duration   int
}

type Doctor struct {
	DoctorID  int
	FullName  string
	Specialty string
}

type PatientInfo struct {
	PatientID int
	FullName  string
	Phone     string
	BirthDate *time.Time
}

type OTPPatient struct {
	PatientID         int64
	FullName          string
	Email             string
	NotSendMailingSMS bool
	SendAutoEmail     bool
}

type LabResult struct {
	ResultID    int
	PatdirecID  int
	GroupName   string
	Code        string
	Name        string
	Value       string
	Unit        string
	Norms       string
	Method      string
	ApprovedBy  string
	ReadyAt     time.Time
	TestComment string
}

type LabPanelRow struct {
	LabResult
	PanelName string
	OrderedAt *time.Time
}

type BookInput struct {
	PlanningID int
	PatientID  int
	ModelsID   int
	MeddepID   int
}
