package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type BookMeResult struct {
	MotconsuID int64
	Restored   bool
}

func BookMe(ctx context.Context, db *sql.DB, planning *PlanningRepo, motconsu *MotconsuRepo, planningID int, patientID int64, defaultModelsID int, now time.Time) (BookMeResult, error) {
	// Important: do NOT wrap clinic-side CreateMotconsu SP into our own transaction.
	// Some installations have triggers that abort the outer transaction.
	// Read slot (allow already held by same patient).
	var plSubjID int
	var dateCons time.Time
	var heure int
	var duree int
	var slotPat sql.NullInt64
	if err := db.QueryRowContext(ctx, `
SELECT PL_SUBJ_ID, DATE_CONS, HEURE, DUREE, PATIENTS_ID
FROM PLANNING
WHERE PLANNING_ID = @id`,
		sql.Named("id", planningID),
	).Scan(&plSubjID, &dateCons, &heure, &duree, &slotPat); err != nil {
		return BookMeResult{}, err
	}

	if slotPat.Valid && slotPat.Int64 != patientID {
		return BookMeResult{}, fmt.Errorf("SLOT_TAKEN")
	}

	dateConsultation := dateCons.Add(time.Duration(heure/100)*time.Hour + time.Duration(heure%100)*time.Minute)
	if dateConsultation.Before(now) {
		return BookMeResult{}, fmt.Errorf("SLOT_IN_PAST")
	}

	// Existing appointment?
	var existingID sql.NullInt64
	var existingStatus sql.NullString
	_ = db.QueryRowContext(ctx, `
SELECT TOP 1 MOTCONSU_ID, REC_STATUS
FROM MOTCONSU
WHERE PLANNING_ID = @pid AND PATIENTS_ID = @pat
ORDER BY MOTCONSU_ID DESC`,
		sql.Named("pid", planningID),
		sql.Named("pat", patientID),
	).Scan(&existingID, &existingStatus)

	if existingID.Valid && existingStatus.Valid {
		switch existingStatus.String {
		case "D":
			res, err := db.ExecContext(ctx, `
UPDATE MOTCONSU SET REC_STATUS='W', KRN_MODIFY_DATE=GETDATE()
WHERE MOTCONSU_ID=@id AND PATIENTS_ID=@pat AND REC_STATUS='D'`,
				sql.Named("id", existingID.Int64),
				sql.Named("pat", patientID),
			); err != nil {
				return BookMeResult{}, fmt.Errorf("restore motconsu: %w", err)
			}
			ra, _ := res.RowsAffected()
			if ra == 0 {
				// Someone changed status between our read and restore.
				return BookMeResult{}, fmt.Errorf("ALREADY_BOOKED")
			}
			return BookMeResult{MotconsuID: existingID.Int64, Restored: true}, nil
		case "W":
			return BookMeResult{}, fmt.Errorf("ALREADY_BOOKED")
		}
	}

	// Determine doctor + dep data from slot.
	var medecinsID, fmDepID int
	var meddepID sql.NullInt64
	if err := db.QueryRowContext(ctx, `
SELECT ps.MEDECINS_ID,
  ISNULL((SELECT FM_DEP_ID FROM MEDDEP WHERE MEDDEP_ID = ps.MEDDEP_ID), 0),
  ps.MEDDEP_ID
FROM PL_SUBJ ps WHERE ps.PL_SUBJ_ID = @id`,
		sql.Named("id", plSubjID),
	).Scan(&medecinsID, &fmDepID, &meddepID); err != nil {
		return BookMeResult{}, fmt.Errorf("doctor data: %w", err)
	}

	modelsID := defaultModelsID
	if modelsID == 0 {
		modelsID = 306
	}
	meddep := 0
	if meddepID.Valid {
		meddep = int(meddepID.Int64)
	}

	var patNom, patPrenom string
	_ = db.QueryRowContext(ctx, `SELECT ISNULL(NOM,''), ISNULL(PRENOM,'') FROM PATIENTS WHERE PATIENTS_ID=@id`,
		sql.Named("id", patientID),
	).Scan(&patNom, &patPrenom)

	var motconsuID int
	if err := db.QueryRowContext(ctx, `
DECLARE @NewID int = 0;
EXEC CreateMotconsu
  @PatientID = @patID, @ModelsID = @modID,
  @MedecinsID = @medID, @FmDepID = @fmID,
  @MeddepID = @depID, @MotconsuEvID = 0,
  @DataTransfersID = 0, @DirAnswID = 0,
  @DateConsultation = @dt,
  @MotconsuID = @NewID OUTPUT;
SELECT @NewID;`,
		sql.Named("patID", patientID),
		sql.Named("modID", modelsID),
		sql.Named("medID", medecinsID),
		sql.Named("fmID", fmDepID),
		sql.Named("depID", meddep),
		sql.Named("dt", dateConsultation),
	).Scan(&motconsuID); err != nil || motconsuID == 0 {
		return BookMeResult{}, fmt.Errorf("CreateMotconsu: %w", err)
	}

	if !slotPat.Valid {
		// Guarded UPDATE: either we fill the slot, or someone else already did.
		res, err := db.ExecContext(ctx, `
UPDATE PLANNING
SET PATIENTS_ID=@patID, NOM=@nom, PRENOM=@prenom
WHERE PLANNING_ID=@planID AND PATIENTS_ID IS NULL`,
			sql.Named("patID", patientID),
			sql.Named("nom", patNom),
			sql.Named("prenom", patPrenom),
			sql.Named("planID", planningID),
		)
		if err != nil {
			return BookMeResult{}, fmt.Errorf("update planning: %w", err)
		}
		ra, _ := res.RowsAffected()
		if ra == 0 {
			// Compensate: booking row exists, but slot was taken concurrently.
			_, _ = db.ExecContext(ctx, `
UPDATE MOTCONSU SET REC_STATUS='D', KRN_MODIFY_DATE=GETDATE()
WHERE MOTCONSU_ID=@id AND PATIENTS_ID=@pat`,
				sql.Named("id", motconsuID),
				sql.Named("pat", patientID),
			)
			return BookMeResult{}, fmt.Errorf("SLOT_TAKEN")
		}
	}
	res, err := db.ExecContext(ctx, `
UPDATE MOTCONSU SET PLANNING_ID=@planID, REC_STATUS=@st
WHERE MOTCONSU_ID=@motID AND PATIENTS_ID=@pat`,
		sql.Named("planID", planningID),
		sql.Named("st", "W"),
		sql.Named("motID", motconsuID),
		sql.Named("pat", patientID),
	)
	if err != nil {
		return BookMeResult{}, fmt.Errorf("update motconsu: %w", err)
	}
	ra, _ := res.RowsAffected()
	if ra == 0 {
		return BookMeResult{}, fmt.Errorf("update motconsu: %w", sql.ErrNoRows)
	}
	return BookMeResult{MotconsuID: int64(motconsuID)}, nil
}

func CancelMe(ctx context.Context, db *sql.DB, motconsuID int64, patientID int64, cancelMinBefore time.Duration, now time.Time) error {
	var patID int64
	var dt time.Time
	var st string
	var planningID sql.NullInt64
	err := db.QueryRowContext(ctx, `
SELECT PATIENTS_ID, DATE_CONSULTATION, ISNULL(REC_STATUS,''), PLANNING_ID
FROM MOTCONSU
WHERE MOTCONSU_ID=@id`,
		sql.Named("id", motconsuID),
	).Scan(&patID, &dt, &st, &planningID)
	if err != nil {
		return err
	}
	if patID != patientID {
		return fmt.Errorf("FORBIDDEN")
	}
	if st == "D" {
		return fmt.Errorf("ALREADY_CANCELLED")
	}
	if st == "A" {
		return fmt.Errorf("CANCEL_NOT_SUPPORTED")
	}
	if !planningID.Valid {
		return fmt.Errorf("CANCEL_NOT_SUPPORTED")
	}
	if dt.Before(now.Add(cancelMinBefore)) {
		return fmt.Errorf("CANCEL_TOO_LATE")
	}
	res, err := db.ExecContext(ctx, `
UPDATE MOTCONSU SET REC_STATUS='D', KRN_MODIFY_DATE=GETDATE()
WHERE MOTCONSU_ID=@id AND PATIENTS_ID=@pat AND REC_STATUS<>'D'`,
		sql.Named("id", motconsuID),
		sql.Named("pat", patientID),
	); err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	ra, _ := res.RowsAffected()
	if ra == 0 {
		return fmt.Errorf("ALREADY_CANCELLED")
	}
	return nil
}
