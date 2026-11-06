package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	mssql "github.com/denisenkom/go-mssqldb"
)

// ProcResult is the transport outcome of one RW batch. The result text is for classification only.
type ProcResult struct {
	Sent         bool
	Finished     bool
	HasRow       bool
	PlanningID   *int64
	Result       string
	State        int
	Err          error
	Precondition bool
}

type AddVisitParams struct {
	PatientID  int64
	PlSubjID   int64
	PlExamID   int64
	VisitStart string
	OrgID      int
	MeddepID   int64
	AuthorID   int64
}

type CancelParams struct {
	PatientID  int64
	PlanningID int64
	OrgID      int
	MeddepID   int64
	AuthorID   int64
}

const addVisitSQL = `
SET NOCOUNT ON;
IF @@TRANCOUNT <> 0 BEGIN RAISERROR('open tran', 16, 1); RETURN; END;
DECLARE @visit datetime = CONVERT(datetime, @visit_s, 126);
DECLARE @r varchar(2048) = '', @pid int = NULL, @msg varchar(2048) = '', @st int = 0;
EXEC dbo.set_session_params @TIME_OFFSET = 0, @LANG = 'rus', @KRN_SYS_SESSION_ID = 0,
     @FM_ORG_ID = @org, @MEDDEP_ID = @meddep, @MEDECINS_ID = @author,
     @TM_SERVICE_CODE = 'M', @DISABLE_TRIGGERS = 0, @SYS_PC_ID = 0;
EXEC dbo.pl_AddVisitContext @PatientID = @patient, @PlSubjID = @subj, @PlExamID = @exam,
     @PlComment = 'Запись из личного кабинета', @VisitDateTime = @visit,
     @Result = @r OUTPUT, @PLANNING_ID = @pid OUTPUT, @CR_WEB = 1, @WEB_USER = 'LK',
     @SourceType = 1, @SOURCE_CODE = 'LK_MEDKVADRAT', @MSG_INFO = @msg OUTPUT, @STATE = @st OUTPUT;
SELECT @pid AS PLANNING_ID, @r AS RESULT, @st AS STATE;`

const cancelVisitSQL = `
SET NOCOUNT ON;
IF @@TRANCOUNT <> 0 BEGIN RAISERROR('open tran', 16, 1); RETURN; END;
DECLARE @r varchar(2048) = '';
EXEC dbo.set_session_params @TIME_OFFSET = 0, @LANG = 'rus', @KRN_SYS_SESSION_ID = 0,
     @FM_ORG_ID = @org, @MEDDEP_ID = @meddep, @MEDECINS_ID = @author,
     @TM_SERVICE_CODE = 'M', @DISABLE_TRIGGERS = 0, @SYS_PC_ID = 0;
EXEC dbo.pl_CancelVisit @PatientID = @patient, @PlanningID = @planning,
     @CancelMotive = 'Отмена пациентом в ЛК', @Result = @r OUTPUT, @Medecins_id = @author;
SELECT @r AS RESULT;`

const rwLanguageSQL = `SELECT @@LANGUAGE`

func classifyRWErr(err error) ProcResult {
	if err == nil {
		return ProcResult{Sent: true, Finished: false}
	}
	var ms mssql.Error
	if errors.As(err, &ms) && strings.Contains(strings.ToLower(ms.Message), "open tran") {
		return ProcResult{Sent: true, Finished: false, Precondition: true, Err: err}
	}
	return ProcResult{Sent: true, Finished: false, Err: err}
}

func ExecAddVisit(ctx context.Context, db *sql.DB, p AddVisitParams) ProcResult {
	if db == nil {
		return ProcResult{Sent: false}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return ProcResult{Sent: false, Err: err}
	}
	defer conn.Close()
	var pid sql.NullInt64
	var result sql.NullString
	var state sql.NullInt64
	err = conn.QueryRowContext(ctx, addVisitSQL,
		sql.Named("visit_s", p.VisitStart),
		sql.Named("org", p.OrgID),
		sql.Named("meddep", p.MeddepID),
		sql.Named("author", p.AuthorID),
		sql.Named("patient", p.PatientID),
		sql.Named("subj", p.PlSubjID),
		sql.Named("exam", p.PlExamID),
	).Scan(&pid, &result, &state)
	if err != nil {
		return classifyRWErr(err)
	}
	out := ProcResult{Sent: true, Finished: true, HasRow: true, Result: result.String, State: int(state.Int64)}
	if pid.Valid {
		v := pid.Int64
		out.PlanningID = &v
	}
	return out
}

func ExecCancelVisit(ctx context.Context, db *sql.DB, p CancelParams) ProcResult {
	if db == nil {
		return ProcResult{Sent: false}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return ProcResult{Sent: false, Err: err}
	}
	defer conn.Close()
	var result sql.NullString
	err = conn.QueryRowContext(ctx, cancelVisitSQL,
		sql.Named("org", p.OrgID),
		sql.Named("meddep", p.MeddepID),
		sql.Named("author", p.AuthorID),
		sql.Named("patient", p.PatientID),
		sql.Named("planning", p.PlanningID),
	).Scan(&result)
	if err != nil {
		return classifyRWErr(err)
	}
	return ProcResult{Sent: true, Finished: true, HasRow: true, Result: result.String}
}

func RWLanguage(ctx context.Context, db *sql.DB) (string, error) {
	var lang string
	err := db.QueryRowContext(ctx, rwLanguageSQL).Scan(&lang)
	return lang, err
}
