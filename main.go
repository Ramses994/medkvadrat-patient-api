package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/joho/godotenv"
)

var (
	db             *sql.DB
	lastModifyDate time.Time
	modifyDateMu   sync.RWMutex // Защита от гонки данных при поллинге
	seenTalons     = make(map[int]struct{}) // <-- ДОБАВЛЕНО: память обработанных талонов
)

// ===== МОДЕЛИ =====

type ScheduleChange struct {
	MotconsuID  int    `json:"motconsu_id"`
	PatientID   int    `json:"patient_id"`
	PatientName string `json:"patient_name"`
	DoctorID    int    `json:"doctor_id"`
	DoctorName  string `json:"doctor_name"`
	DateConsult string `json:"date_consultation"`
	ModifyDate  string `json:"modify_date"`
}

type FreeSlot struct {
	PlanningID int    `json:"planning_id"`
	DoctorName string `json:"doctor_name"`
	PlSubjID   int    `json:"pl_subj_id"`
	Date       string `json:"date"`
	Time       string `json:"time"`
	Duration   int    `json:"duration_min"`
}

type BookRequest struct {
	PlanningID int `json:"planning_id"`
	PatientID  int `json:"patient_id"`
	ModelsID   int `json:"models_id"`
	MeddepID   int `json:"meddep_id"`
}

type BookResponse struct {
	Success    bool   `json:"success"`
	MotconsuID int    `json:"motconsu_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Doctor struct {
	DoctorID    int    `json:"doctor_id"`
	FullName    string `json:"full_name"`
	Specialty   string `json:"specialty"`
	IsAvailable bool   `json:"is_available"`
}

type PatientInfo struct {
	PatientID int    `json:"patient_id"`
	FullName  string `json:"full_name"`
	Phone     string `json:"phone"`
	BirthDate string `json:"birth_date"`
}

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type LabResult struct {
	ResultID    int    `json:"result_id"`
	PatdirecID  int    `json:"patdirec_id"`
	GroupName   string `json:"group_name,omitempty"`
	Code        string `json:"code,omitempty"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	Unit        string `json:"unit,omitempty"`
	Norms       string `json:"norms,omitempty"`
	InRange     *bool  `json:"in_range,omitempty"`
	Method      string `json:"method,omitempty"`
	ApprovedBy  string `json:"approved_by,omitempty"`
	ReadyAt     string `json:"ready_at"`
	TestComment string `json:"test_comment,omitempty"`
}

type LabPanel struct {
	PatdirecID    int         `json:"patdirec_id"`
	PanelName     string      `json:"panel_name"`
	OrderedAt     string      `json:"ordered_at,omitempty"`
	ReadyAt       string      `json:"ready_at"`
	readyAtRaw    time.Time   `json:"-"` // Скрытое поле для точной сортировки
	TestsCount    int         `json:"tests_count"`
	HasOutOfRange bool        `json:"has_out_of_range"`
	Tests         []LabResult `json:"tests"`
}

// ===== MAIN =====

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Файл .env не найден, используются системные переменные окружения")
	}

	connString := fmt.Sprintf(
		"server=%s;port=%s;database=%s;user id=%s;password=%s;encrypt=disable",
		os.Getenv("DB_SERVER"),
		os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
	)

	var err error
	db, err = sql.Open("sqlserver", connString)
	if err != nil {
		log.Fatalf("Ошибка подключения к БД: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("БД недоступна: %v", err)
	}
	log.Println("Подключение к Medialog MSSQL установлено")

	var initialLastDate time.Time
	row := db.QueryRow("SELECT ISNULL(MAX(KRN_MODIFY_DATE), GETDATE()) FROM MOTCONSU")
	if err := row.Scan(&initialLastDate); err != nil {
		log.Fatalf("Ошибка получения стартовой даты: %v", err)
	}
	
	modifyDateMu.Lock()
	lastModifyDate = initialLastDate
	modifyDateMu.Unlock()
	
	log.Printf("Поллинг MOTCONSU с %s", initialLastDate.Format("2006-01-02 15:04:05"))

	go pollLoop()

	// Регистрация роутов (все защищены authMiddleware, кроме /health)
	http.HandleFunc("/api/schedule/changes", authMiddleware(handleChanges))
	http.HandleFunc("/api/schedule/slots", authMiddleware(handleFreeSlots))
	http.HandleFunc("/api/schedule/book", authMiddleware(handleBook))
	http.HandleFunc("/api/doctors", authMiddleware(handleDoctors))
	http.HandleFunc("/api/patients/search", authMiddleware(handlePatientSearch))
	http.HandleFunc("/api/patients/lab-results", authMiddleware(handlePatientLabResults))
	http.HandleFunc("/api/patients/lab-panels", authMiddleware(handlePatientLabPanels))
	http.HandleFunc("/api/health", handleHealth)

	apiPort := os.Getenv("API_PORT")
	if apiPort == "" {
		apiPort = ":8080"
	}

	log.Printf("REST API запущен на %s", apiPort)
	log.Fatal(http.ListenAndServe(apiPort, nil))
}

// ===== AUTH MIDDLEWARE =====

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("API_TOKEN")
		if token != "" {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer "+token {
				jsonResp(w, http.StatusUnauthorized, APIResponse{Error: "Unauthorized"})
				return
			}
		}
		next(w, r)
	}
}

// ===== ПОЛЛИНГ =====

func pollLoop() {
	for {
		pollMotconsu()
		time.Sleep(5 * time.Second)
	}
}

func pollMotconsu() {
	modifyDateMu.RLock()
	currentLast := lastModifyDate
	modifyDateMu.RUnlock()

	query := `
		SELECT m.MOTCONSU_ID, m.PATIENTS_ID, m.MEDECINS_ID,
			m.DATE_CONSULTATION, m.KRN_MODIFY_DATE,
			ISNULL(p.NOM,'') AS PAT_NOM, ISNULL(p.PRENOM,'') AS PAT_PRENOM,
			ISNULL(d.NOM,'') AS DOC_NOM, ISNULL(d.PRENOM,'') AS DOC_PRENOM
		FROM MOTCONSU m
		LEFT JOIN PATIENTS p ON p.PATIENTS_ID = m.PATIENTS_ID
		LEFT JOIN MEDECINS d ON d.MEDECINS_ID = m.MEDECINS_ID
		WHERE m.KRN_MODIFY_DATE > @lastDate
		ORDER BY m.KRN_MODIFY_DATE ASC`

	rows, err := db.Query(query, sql.Named("lastDate", currentLast))
	if err != nil {
		log.Printf("Ошибка поллинга: %v", err)
		return
	}
	defer rows.Close()

	newLast := currentLast

	for rows.Next() {
		var motID, patID, medID int
		var dateConsult, modifyDate time.Time
		var patNom, patPrenom, docNom, docPrenom string

		if err := rows.Scan(&motID, &patID, &medID, &dateConsult, &modifyDate,
			&patNom, &patPrenom, &docNom, &docPrenom); err != nil {
			log.Printf("scan error (polling): %v", err)
			continue
		}

// <-- ДОБАВЛЕНА ПРОВЕРКА НА СПАМ -->
		if _, exists := seenTalons[motID]; !exists {
			log.Printf("━━━ Талон %d | %s %s → %s %s | %s ━━━",
				motID, patNom, patPrenom, docNom, docPrenom,
				dateConsult.Format("02.01.2006 15:04"))
			
			seenTalons[motID] = struct{}{} // Запоминаем этот ID
		}

		if modifyDate.After(newLast) {
			newLast = modifyDate
		}

	if newLast.After(currentLast) {
		modifyDateMu.Lock()
		lastModifyDate = newLast
		modifyDateMu.Unlock()
	}
}

// ===== API: Изменения расписания =====

func handleChanges(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	if since == "" {
		since = time.Now().Add(-1 * time.Hour).Format("2006-01-02T15:04:05")
	}

	sinceTime, err := time.Parse("2006-01-02T15:04:05", since)
	if err != nil {
		jsonResp(w, 400, APIResponse{Error: "Формат since: 2006-01-02T15:04:05"})
		return
	}

	query := `
		SELECT m.MOTCONSU_ID, m.PATIENTS_ID, m.MEDECINS_ID,
			m.DATE_CONSULTATION, m.KRN_MODIFY_DATE,
			ISNULL(p.NOM,'') + ' ' + ISNULL(p.PRENOM,'') AS PAT_NAME,
			ISNULL(d.NOM,'') + ' ' + ISNULL(d.PRENOM,'') AS DOC_NAME
		FROM MOTCONSU m
		LEFT JOIN PATIENTS p ON p.PATIENTS_ID = m.PATIENTS_ID
		LEFT JOIN MEDECINS d ON d.MEDECINS_ID = m.MEDECINS_ID
		WHERE m.KRN_MODIFY_DATE > @since
		ORDER BY m.KRN_MODIFY_DATE DESC`

	rows, err := db.QueryContext(r.Context(), query, sql.Named("since", sinceTime))
	if err != nil {
		jsonResp(w, 500, APIResponse{Error: err.Error()})
		return
	}
	defer rows.Close()

	var changes []ScheduleChange
	for rows.Next() {
		var c ScheduleChange
		var dateConsult, modifyDate time.Time
		if err := rows.Scan(&c.MotconsuID, &c.PatientID, &c.DoctorID,
			&dateConsult, &modifyDate, &c.PatientName, &c.DoctorName); err != nil {
			log.Printf("scan error (changes): %v", err)
			continue
		}
		c.DateConsult = dateConsult.Format("2006-01-02 15:04")
		c.ModifyDate = modifyDate.Format("2006-01-02 15:04:05")
		changes = append(changes, c)
	}

	jsonResp(w, 200, APIResponse{Success: true, Data: changes})
}

// ===== API: Свободные слоты =====

func handleFreeSlots(w http.ResponseWriter, r *http.Request) {
	doctorID := r.URL.Query().Get("doctor_id")
	date := r.URL.Query().Get("date")

	if doctorID == "" || date == "" {
		jsonResp(w, 400, APIResponse{Error: "Нужны doctor_id и date (YYYY-MM-DD)"})
		return
	}

	query := `
		SELECT p.PLANNING_ID, ps.NAME, ps.PL_SUBJ_ID,
			p.DATE_CONS, p.HEURE, p.DUREE
		FROM PLANNING p
		JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
		WHERE ps.MEDECINS_ID = @doctorID
			AND p.DATE_CONS >= @dateStart AND p.DATE_CONS < DATEADD(day,1,@dateStart)
			AND p.PATIENTS_ID IS NULL
			AND p.STATUS = 0
		ORDER BY p.HEURE`

	rows, err := db.QueryContext(r.Context(), query,
		sql.Named("doctorID", doctorID),
		sql.Named("dateStart", date))
	if err != nil {
		jsonResp(w, 500, APIResponse{Error: err.Error()})
		return
	}
	defer rows.Close()

	var slots []FreeSlot
	for rows.Next() {
		var s FreeSlot
		var dateCons time.Time
		var heure int
		if err := rows.Scan(&s.PlanningID, &s.DoctorName, &s.PlSubjID,
			&dateCons, &heure, &s.Duration); err != nil {
			log.Printf("scan error (slots): %v", err)
			continue
		}
		s.Date = dateCons.Format("2006-01-02")
		s.Time = fmt.Sprintf("%02d:%02d", heure/100, heure%100)
		slots = append(slots, s)
	}

	jsonResp(w, 200, APIResponse{Success: true, Data: slots})
}

// ===== API: Создать запись =====

func handleBook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonResp(w, 405, APIResponse{Error: "POST only"})
		return
	}

	var req BookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResp(w, 400, APIResponse{Error: "Невалидный JSON"})
		return
	}

	if req.PlanningID == 0 || req.PatientID == 0 {
		jsonResp(w, 400, APIResponse{Error: "planning_id и patient_id обязательны"})
		return
	}

	// 1. Открываем транзакцию
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		jsonResp(w, 500, BookResponse{Error: "Failed to start transaction: " + err.Error()})
		return
	}
	defer tx.Rollback()

	// 2. Блокируем строку PLANNING (WITH UPDLOCK, HOLDLOCK)
	var plSubjID, heure int
	var dateCons time.Time
	err = tx.QueryRowContext(r.Context(), `
		SELECT PL_SUBJ_ID, DATE_CONS, HEURE
		FROM PLANNING WITH (UPDLOCK, HOLDLOCK)
		WHERE PLANNING_ID = @id AND PATIENTS_ID IS NULL`,
		sql.Named("id", req.PlanningID)).Scan(&plSubjID, &dateCons, &heure)
	if err != nil {
		jsonResp(w, 404, BookResponse{Error: "Слот не найден или уже занят"})
		return
	}

	var medecinsID, fmDepID int
	var meddepID sql.NullInt64
	err = tx.QueryRowContext(r.Context(), `
		SELECT ps.MEDECINS_ID,
			ISNULL((SELECT FM_DEP_ID FROM MEDDEP WHERE MEDDEP_ID = ps.MEDDEP_ID), 0),
			ps.MEDDEP_ID
		FROM PL_SUBJ ps WHERE ps.PL_SUBJ_ID = @id`,
		sql.Named("id", plSubjID)).Scan(&medecinsID, &fmDepID, &meddepID)
	if err != nil {
		jsonResp(w, 500, BookResponse{Error: "Ошибка данных врача: " + err.Error()})
		return
	}

	dateConsultation := dateCons.Add(
		time.Duration(heure/100)*time.Hour + time.Duration(heure%100)*time.Minute)

	// Получаем дефолтный MODELS_ID из окружения
	modelsID := req.ModelsID
	if modelsID == 0 {
		if envModels := os.Getenv("DEFAULT_MODELS_ID"); envModels != "" {
			modelsID, _ = strconv.Atoi(envModels)
		}
		if modelsID == 0 {
			modelsID = 306
		}
	}

	meddep := 0
	if req.MeddepID > 0 {
		meddep = req.MeddepID
	} else if meddepID.Valid {
		meddep = int(meddepID.Int64)
	}

	var patNom, patPrenom string
	tx.QueryRowContext(r.Context(), "SELECT ISNULL(NOM,''), ISNULL(PRENOM,'') FROM PATIENTS WHERE PATIENTS_ID = @id",
		sql.Named("id", req.PatientID)).Scan(&patNom, &patPrenom)

	var motconsuID int
	err = tx.QueryRowContext(r.Context(), `
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
		jsonResp(w, 500, BookResponse{Error: fmt.Sprintf("Ошибка CreateMotconsu: %v", err)})
		return
	}

	_, err = tx.ExecContext(r.Context(), `
		UPDATE PLANNING SET PATIENTS_ID = @patID, NOM = @nom, PRENOM = @prenom
		WHERE PLANNING_ID = @planID`,
		sql.Named("patID", req.PatientID),
		sql.Named("nom", patNom),
		sql.Named("prenom", patPrenom),
		sql.Named("planID", req.PlanningID))
	if err != nil {
		jsonResp(w, 500, BookResponse{Error: "Ошибка UPDATE PLANNING: " + err.Error()})
		return
	}

	_, err = tx.ExecContext(r.Context(), `
		UPDATE MOTCONSU SET PLANNING_ID = @planID, REC_STATUS = 'A'
		WHERE MOTCONSU_ID = @motID`,
		sql.Named("planID", req.PlanningID),
		sql.Named("motID", motconsuID))
	if err != nil {
		jsonResp(w, 500, BookResponse{Error: "Ошибка UPDATE MOTCONSU: " + err.Error()})
		return
	}

	// 3. Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		jsonResp(w, 500, BookResponse{Error: "Commit failed: " + err.Error()})
		return
	}

	log.Printf("✅ Запись создана: MOTCONSU=%d, PLANNING=%d, Пациент=%s %s",
		motconsuID, req.PlanningID, patNom, patPrenom)

	jsonResp(w, 200, BookResponse{Success: true, MotconsuID: motconsuID})
}

// ===== API: Список врачей =====

func handleDoctors(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT MEDECINS_ID,
			ISNULL(NOM,'') + ' ' + ISNULL(PRENOM,'') AS FULL_NAME,
			'' AS SPECIALTY 
		FROM MEDECINS
		WHERE ISNULL(NOM,'') != ''
		ORDER BY NOM`

	rows, err := db.QueryContext(r.Context(), query)
	if err != nil {
		jsonResp(w, 500, APIResponse{Error: err.Error()})
		return
	}
	defer rows.Close()

	var doctors []Doctor
	for rows.Next() {
		var d Doctor
		if err := rows.Scan(&d.DoctorID, &d.FullName, &d.Specialty); err != nil {
			log.Printf("scan error (doctors): %v", err)
			continue
		}
		d.IsAvailable = true
		doctors = append(doctors, d)
	}

	jsonResp(w, 200, APIResponse{Success: true, Data: doctors})
}

// ===== API: Поиск пациента =====

func handlePatientSearch(w http.ResponseWriter, r *http.Request) {
	phone := r.URL.Query().Get("phone")
	if phone == "" {
		jsonResp(w, 400, APIResponse{Error: "Параметр phone обязателен"})
		return
	}

	cleanPhone := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)

	searchPhone := cleanPhone
	if len(cleanPhone) > 10 {
		searchPhone = cleanPhone[len(cleanPhone)-10:]
	}

	query := `
		SELECT PATIENTS_ID,
			ISNULL(NOM,'') + ' ' + ISNULL(PRENOM,'') AS FULL_NAME,
			ISNULL(MOBIL_TELEFON, ISNULL(TEL, ISNULL(RAB_TEL,''))) AS PHONE,
			NULL AS BIRTH_DATE
		FROM PATIENTS
		WHERE REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(MOBIL_TELEFON,''),'+',''),'-',''),' ',''),'(','') LIKE '%' + @phone + '%'
		   OR REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(TEL,''),'+',''),'-',''),' ',''),'(','') LIKE '%' + @phone + '%'
		   OR REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(RAB_TEL,''),'+',''),'-',''),' ',''),'(','') LIKE '%' + @phone + '%'`

	rows, err := db.QueryContext(r.Context(), query, sql.Named("phone", searchPhone))
	if err != nil {
		jsonResp(w, 500, APIResponse{Error: err.Error()})
		return
	}
	defer rows.Close()

	var patients []PatientInfo
	for rows.Next() {
		var p PatientInfo
		var birthDate sql.NullTime
		if err := rows.Scan(&p.PatientID, &p.FullName, &p.Phone, &birthDate); err != nil {
			log.Printf("scan error (patient search): %v", err)
			continue
		}
		if birthDate.Valid {
			p.BirthDate = birthDate.Time.Format("2006-01-02")
		}
		patients = append(patients, p)
	}

	jsonResp(w, 200, APIResponse{Success: true, Data: patients})
}

// ===== API: Результаты анализов (плоский список) =====

func handlePatientLabResults(w http.ResponseWriter, r *http.Request) {
	patientID := r.URL.Query().Get("patient_id")
	if patientID == "" {
		jsonResp(w, 400, APIResponse{Error: "patient_id обязателен"})
		return
	}

	daysBack := 0
	if s := r.URL.Query().Get("days_back"); s != "" {
		fmt.Sscanf(s, "%d", &daysBack)
	}

	query := `
		SELECT
			LAB_ANT_RESULTS_ID, ISNULL(PATDIREC_ID,0) AS PATDIREC_ID,
			ISNULL(GROUP_NAME,'')   AS GROUP_NAME,
			ISNULL(CODE,'')         AS CODE,
			ISNULL(NAME,'')         AS NAME,
			ISNULL(VALUE,'')        AS VALUE,
			ISNULL(UNIT_NAME,'')    AS UNIT_NAME,
			ISNULL(NORMS,'')        AS NORMS,
			ISNULL(METHOD_NAME,'')  AS METHOD_NAME,
			ISNULL(APPROVING_DOCTOR,'') AS APPROVING_DOCTOR,
			KRN_CREATE_DATE,
			ISNULL(CAST(TEST_COMMENT AS varchar(4000)),'') AS TEST_COMMENT
		FROM LAB_ANT_RESULTS
		WHERE PATIENTS_ID = @patID
		  AND (@days = 0 OR KRN_CREATE_DATE >= DATEADD(day, -@days, GETDATE()))
		ORDER BY KRN_CREATE_DATE DESC, PATDIREC_ID DESC, GROUP_NAME, NAME`

	rows, err := db.QueryContext(r.Context(), query,
		sql.Named("patID", patientID),
		sql.Named("days", daysBack))
	if err != nil {
		jsonResp(w, 500, APIResponse{Error: err.Error()})
		return
	}
	defer rows.Close()

	var results []LabResult
	for rows.Next() {
		var x LabResult
		var readyAt time.Time
		if err := rows.Scan(
			&x.ResultID, &x.PatdirecID, &x.GroupName, &x.Code, &x.Name,
			&x.Value, &x.Unit, &x.Norms, &x.Method, &x.ApprovedBy,
			&readyAt, &x.TestComment,
		); err != nil {
			log.Printf("scan error (lab results): %v", err)
			continue
		}
		x.ReadyAt = readyAt.Format("2006-01-02 15:04")
		if inRange, ok := checkInRange(x.Value, x.Norms); ok {
			x.InRange = &inRange
		}
		results = append(results, x)
	}

	jsonResp(w, 200, APIResponse{Success: true, Data: results})
}

// ===== API: Панели анализов (сгруппированные) =====

func handlePatientLabPanels(w http.ResponseWriter, r *http.Request) {
	patientID := r.URL.Query().Get("patient_id")
	if patientID == "" {
		jsonResp(w, 400, APIResponse{Error: "patient_id обязателен"})
		return
	}

	daysBack := 0
	if s := r.URL.Query().Get("days_back"); s != "" {
		fmt.Sscanf(s, "%d", &daysBack)
	}

	query := `
		SELECT
			r.LAB_ANT_RESULTS_ID,
			ISNULL(r.PATDIREC_ID,0) AS PATDIREC_ID,
			ISNULL(LEFT(CAST(p.DESCRIPTION AS varchar(500)), 250),'') AS PANEL_NAME,
			p.CREATE_DATE_TIME AS ORDERED_AT,
			ISNULL(r.GROUP_NAME,'')   AS GROUP_NAME,
			ISNULL(r.CODE,'')         AS CODE,
			ISNULL(r.NAME,'')         AS NAME,
			ISNULL(r.VALUE,'')        AS VALUE,
			ISNULL(r.UNIT_NAME,'')    AS UNIT_NAME,
			ISNULL(r.NORMS,'')        AS NORMS,
			ISNULL(r.METHOD_NAME,'')  AS METHOD_NAME,
			ISNULL(r.APPROVING_DOCTOR,'') AS APPROVING_DOCTOR,
			r.KRN_CREATE_DATE,
			ISNULL(CAST(r.TEST_COMMENT AS varchar(4000)),'') AS TEST_COMMENT
		FROM LAB_ANT_RESULTS r
		LEFT JOIN PATDIREC p ON p.PATDIREC_ID = r.PATDIREC_ID
		WHERE r.PATIENTS_ID = @patID
		  AND (@days = 0 OR r.KRN_CREATE_DATE >= DATEADD(day, -@days, GETDATE()))
		ORDER BY r.PATDIREC_ID DESC, r.GROUP_NAME, r.NAME`

	rows, err := db.QueryContext(r.Context(), query,
		sql.Named("patID", patientID),
		sql.Named("days", daysBack))
	if err != nil {
		jsonResp(w, 500, APIResponse{Error: err.Error()})
		return
	}
	defer rows.Close()

	panelMap := map[int]*LabPanel{}
	panelOrder := []int{}

	for rows.Next() {
		var rid, pid int
		var panelName, group, code, name, value, unit, norms, method, appr, comment string
		var orderedAt sql.NullTime
		var readyAt time.Time

		if err := rows.Scan(
			&rid, &pid, &panelName, &orderedAt,
			&group, &code, &name, &value, &unit, &norms,
			&method, &appr, &readyAt, &comment,
		); err != nil {
			log.Printf("scan error (lab panels): %v", err)
			continue
		}

		test := LabResult{
			ResultID:    rid,
			PatdirecID:  pid,
			GroupName:   group,
			Code:        code,
			Name:        name,
			Value:       value,
			Unit:        unit,
			Norms:       norms,
			Method:      method,
			ApprovedBy:  appr,
			ReadyAt:     readyAt.Format("2006-01-02 15:04"),
			TestComment: comment,
		}
		if inRange, ok := checkInRange(value, norms); ok {
			test.InRange = &inRange
		}

		panel, exists := panelMap[pid]
		if !exists {
			panel = &LabPanel{
				PatdirecID: pid,
				PanelName:  panelName,
				ReadyAt:    readyAt.Format("2006-01-02 15:04"),
				readyAtRaw: readyAt,
			}
			if orderedAt.Valid {
				panel.OrderedAt = orderedAt.Time.Format("2006-01-02 15:04")
			}
			panelMap[pid] = panel
			panelOrder = append(panelOrder, pid)
		}

		panel.Tests = append(panel.Tests, test)
		panel.TestsCount++
		
		if readyAt.After(panel.readyAtRaw) {
			panel.readyAtRaw = readyAt
			panel.ReadyAt = test.ReadyAt
		}
		
		if test.InRange != nil && !*test.InRange {
			panel.HasOutOfRange = true
		}
	}

	panels := make([]LabPanel, 0, len(panelOrder))
	for _, pid := range panelOrder {
		panels = append(panels, *panelMap[pid])
	}
	
	// Точная сортировка по сырому времени
	sort.Slice(panels, func(i, j int) bool {
		return panels[i].readyAtRaw.After(panels[j].readyAtRaw)
	})

	jsonResp(w, 200, APIResponse{Success: true, Data: panels})
}

// ===== API: Health =====

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		jsonResp(w, 500, APIResponse{Error: "DB unavailable"})
		return
	}
	jsonResp(w, 200, APIResponse{Success: true, Data: "OK"})
}

// ===== HELPERS =====

func jsonResp(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func checkInRange(valueStr, normsStr string) (bool, bool) {
	if valueStr == "" || normsStr == "" {
		return false, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(valueStr), ",", "."), 64)
	if err != nil {
		return false, false
	}
	parts := strings.Split(strings.ReplaceAll(normsStr, ",", "."), "-")
	if len(parts) != 2 {
		return false, false
	}
	lo, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	hi, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return false, false
	}
	return v >= lo && v <= hi, true
}