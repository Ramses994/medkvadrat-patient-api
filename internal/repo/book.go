package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type BookingRepo struct {
	db       *sql.DB
	planning *PlanningRepo
}

// Left as part of Planning/Motconsu/Patient repos in step 1.

type BookResult struct {
	MotconsuID int
}

func Book(ctx context.Context, db *sql.DB, planning *PlanningRepo, motconsu *MotconsuRepo, req BookInput, defaultModelsID int) (BookResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return BookResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	locked, err := planning.LockFreeSlot(tx, ctx, req.PlanningID)
	if err != nil {
		return BookResult{}, err
	}

	var medecinsID, fmDepID int
	var meddepID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT ps.MEDECINS_ID,
			ISNULL((SELECT FM_DEP_ID FROM MEDDEP WHERE MEDDEP_ID = ps.MEDDEP_ID), 0),
			ps.MEDDEP_ID
		FROM PL_SUBJ ps WHERE ps.PL_SUBJ_ID = @id`,
		sql.Named("id", locked.PlSubjID),
	).Scan(&medecinsID, &fmDepID, &meddepID)
	if err != nil {
		return BookResult{}, fmt.Errorf("doctor data: %w", err)
	}

	dateConsultation := locked.DateCons.Add(
		time.Duration(locked.Heure/100)*time.Hour + time.Duration(locked.Heure%100)*time.Minute)

	modelsID := req.ModelsID
	if modelsID == 0 {
		modelsID = defaultModelsID
	}
	if modelsID == 0 {
		modelsID = 306
	}

	meddep := 0
	if req.MeddepID > 0 {
		meddep = req.MeddepID
	} else if meddepID.Valid {
		meddep = int(meddepID.Int64)
	}

	var patNom, patPrenom string
	_ = tx.QueryRowContext(ctx, "SELECT ISNULL(NOM,''), ISNULL(PRENOM,'') FROM PATIENTS WHERE PATIENTS_ID = @id",
		sql.Named("id", req.PatientID),
	).Scan(&patNom, &patPrenom)

	var motconsuID int
	err = tx.QueryRowContext(ctx, `
		DECLARE @NewID int = 0;
		EXEC CreateMotconsu
			@PatientID = @patID, @ModelsID = @modID,
			@MedecinsID = @medID, @FmDepID = @fmID,
			@MeddepID = @depID, @MotconsuEvID = 0,
			@DataTransfersID = 0, @DirAnswID = 0,
			@DateConsultation = @dt,
			@MotconsuID = @NewID OUTPUT;
		SELECT @NewID;`,
		sql.Named("patID", req.PatientID),
		sql.Named("modID", modelsID),
		sql.Named("medID", medecinsID),
		sql.Named("fmID", fmDepID),
		sql.Named("depID", meddep),
		sql.Named("dt", dateConsultation),
	).Scan(&motconsuID)
	if err != nil || motconsuID == 0 {
		return BookResult{}, fmt.Errorf("CreateMotconsu: %w", err)
	}

	if err := planning.FillSlot(tx, ctx, req.PlanningID, req.PatientID, patNom, patPrenom); err != nil {
		return BookResult{}, fmt.Errorf("update planning: %w", err)
	}
	if err := motconsu.SetPlanningAndStatus(tx, ctx, motconsuID, req.PlanningID, "A"); err != nil {
		return BookResult{}, fmt.Errorf("update motconsu: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return BookResult{}, fmt.Errorf("commit: %w", err)
	}
	return BookResult{MotconsuID: motconsuID}, nil
}
