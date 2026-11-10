package booking

import (
	"net/http"
	"regexp"
	"strings"
)

const (
	StateRejected  = "rejected"
	StateUnknown   = "unknown"
	StateCandidate = "candidate"
	StateOK        = "ok"
	StatePartial   = "partial"
)

// Outcome is one row of the booking result table. Result text is not kept.
type Outcome struct {
	Code         string
	HTTPStatus   int
	State        string
	LogProcError bool
	LogUnmapped  bool
	ResultLen    int
}

var templates = map[string]string{
	"001": "Дата приема выходит за границы допустимого периода",
	"002": `Не передан обязательный параметр для записи в расписание "вид приема"`,
	"003": `Не передан обязательный параметр для записи в расписание "ИД расписания"`,
	"004": "Не допускается запись в расписание по направлению с одновременной опцией автоматического подбора направлений",
	"005": "Автоподбор направлений не возможен для анонимной записи",
	"006": "Parameter @CR_MEDECIN_NOM is empty",
	"007": "Превышено максимальное количество приемов в день",
	"008": "Превышено максимальное количество предварительных записей на прием",
	"009": "Запись пересекается по времени с другим приемом врача",
	"010": "Запись пересекается по времени с другим приемом пациента",
	"011": "Запись выходит за границы рабочего времени врача",
	"012": "Запись пересекается по времени с другим приемом врача",
	"013": "Запись на это время временно заблокирована. Выберите другое время приема либо повторите попытку позже.",
	"014": "Запись пересекается по времени с исключительным событием",
	"015": "Запись пересекается по времени с плановым событием",
	"016": "Запись пересекается по времени с сегментом типового дня, запись в который запрещена",
	"017": "Для расписания PL_SUBJ_ID=%1 не доступна запись по виду приема PL_EXAM_ID=%2",
	"019": "Запись по направлению PATDIREC_ID=%1 не допустима в расписание",
	"020": `User with NOM = "%1" not found in table MEDECINS`,
	"021": "Нельзя создать более %1 записей в день для одного пациента к этому врачу",
	"022": "Произошла ошибка при выполнении записи в расписание - %1",
}

// Template returns the S35 text for a code. Tests use it to build samples.
func Template(code string) string { return templates[code] }

var placeholder = regexp.MustCompile(`%\d+`)

func matchTemplate(tmpl, text string) ([]string, bool) {
	quoted := regexp.QuoteMeta(tmpl)
	pat := placeholder.ReplaceAllString(quoted, `(.*?)`)
	rx, err := regexp.Compile("^" + pat + "$")
	if err != nil {
		return nil, false
	}
	m := rx.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return nil, false
	}
	return m[1:], true
}

func matches(code, text string) (string, bool) {
	groups, ok := matchTemplate(templates[code], text)
	if !ok {
		return "", false
	}
	if len(groups) == 0 {
		return "", true
	}
	return groups[0], true
}

func anyCode(text string, codes ...string) bool {
	for _, code := range codes {
		if _, ok := matches(code, text); ok {
			return true
		}
	}
	return false
}

// Classify applies the result table in order. planningID nil means SQL NULL.
func Classify(planningID *int64, result string, state int) Outcome {
	text := strings.TrimSpace(result)
	if state == 2 {
		return Outcome{Code: "BOOKING_REJECTED", HTTPStatus: http.StatusUnprocessableEntity, State: StateRejected}
	}
	if p1, ok := matches("022", text); ok {
		if strings.Contains(p1, "Cannot insert the value NULL into column 'PLANNING_ID'") && strings.Contains(p1, "PLANNING_USER_EXT") {
			return Outcome{Code: "SLOT_TAKEN", HTTPStatus: http.StatusConflict, State: StateRejected}
		}
		return Outcome{Code: "BOOKING_ERROR", HTTPStatus: http.StatusInternalServerError, State: StateRejected, LogProcError: true}
	}
	if anyCode(text, "009", "012") {
		return Outcome{Code: "SLOT_TAKEN", HTTPStatus: http.StatusConflict, State: StateRejected}
	}
	if anyCode(text, "013") {
		return Outcome{Code: "SLOT_LOCKED", HTTPStatus: http.StatusConflict, State: StateRejected}
	}
	if anyCode(text, "010") {
		return Outcome{Code: "PATIENT_TIME_CONFLICT", HTTPStatus: http.StatusConflict, State: StateRejected}
	}
	if anyCode(text, "001", "011", "014", "015", "016") {
		return Outcome{Code: "SLOT_UNAVAILABLE", HTTPStatus: http.StatusConflict, State: StateRejected}
	}
	if anyCode(text, "007", "008", "021") {
		return Outcome{Code: "LIMIT_MIS", HTTPStatus: http.StatusUnprocessableEntity, State: StateRejected}
	}
	if anyCode(text, "017", "002", "003", "004", "005", "006", "019", "020") {
		return Outcome{Code: "BOOKING_ERROR", HTTPStatus: http.StatusInternalServerError, State: StateRejected, LogProcError: true}
	}
	if text != "" {
		return Outcome{Code: "BOOKING_UNKNOWN", HTTPStatus: http.StatusServiceUnavailable, State: StateUnknown, LogUnmapped: true, ResultLen: len(text)}
	}
	if planningID != nil && *planningID > 0 {
		return Outcome{HTTPStatus: http.StatusCreated, State: StateCandidate}
	}
	return Outcome{Code: "BOOKING_UNKNOWN", HTTPStatus: http.StatusServiceUnavailable, State: StateUnknown}
}

// LanguageOK is the RW login language required for the English fragments inside code 022.
func LanguageOK(lang string) bool {
	return strings.EqualFold(strings.TrimSpace(lang), "us_english")
}
