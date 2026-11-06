package booking

import (
	"strconv"
	"strings"
	"testing"
)

func fill(tmpl string, args ...string) string {
	out := tmpl
	for i, arg := range args {
		out = strings.ReplaceAll(out, "%"+strconv.Itoa(i+1), arg)
	}
	return out
}

func TestClassify_Table(t *testing.T) {
	id := int64(10)
	cases := []struct {
		name   string
		id     *int64
		result string
		state  int
		code   string
		st     string
	}{
		{"state2 empty", &id, "", 2, "BOOKING_REJECTED", StateRejected},
		{"state2 text", &id, fill(Template("009")), 2, "BOOKING_REJECTED", StateRejected},
		{"022 race", nil, fill(Template("022"), "Cannot insert the value NULL into column 'PLANNING_ID', table 'Medialog.dbo.PLANNING_USER_EXT'"), 0, "SLOT_TAKEN", StateRejected},
		{"022 race with id", &id, fill(Template("022"), "Cannot insert the value NULL into column 'PLANNING_ID', table 'x.PLANNING_USER_EXT'"), 0, "SLOT_TAKEN", StateRejected},
		{"022 meddep", nil, fill(Template("022"), "Cannot INSERT PLANNING because MEDDEP does not exist"), 0, "BOOKING_ERROR", StateRejected},
		{"022 nesting", nil, fill(Template("022"), "Maximum stored procedure, function, trigger, or view nesting level exceeded (limit 32)"), 0, "BOOKING_ERROR", StateRejected},
		{"022 russian 515", nil, fill(Template("022"), `Не удается вставить значение NULL в столбец "PLANNING_ID", таблицы dbo.PLANNING_USER_EXT; ошибка 515`), 0, "BOOKING_ERROR", StateRejected},
		{"009", &id, fill(Template("009")), 0, "SLOT_TAKEN", StateRejected},
		{"012", nil, fill(Template("012")), 0, "SLOT_TAKEN", StateRejected},
		{"013", nil, fill(Template("013")), 0, "SLOT_LOCKED", StateRejected},
		{"010", nil, fill(Template("010")), 0, "PATIENT_TIME_CONFLICT", StateRejected},
		{"001", nil, fill(Template("001")), 0, "SLOT_UNAVAILABLE", StateRejected},
		{"011", nil, fill(Template("011")), 0, "SLOT_UNAVAILABLE", StateRejected},
		{"014", nil, fill(Template("014")), 0, "SLOT_UNAVAILABLE", StateRejected},
		{"015", nil, fill(Template("015")), 0, "SLOT_UNAVAILABLE", StateRejected},
		{"016", nil, fill(Template("016")), 0, "SLOT_UNAVAILABLE", StateRejected},
		{"007", nil, fill(Template("007")), 0, "LIMIT_MIS", StateRejected},
		{"008", nil, fill(Template("008")), 0, "LIMIT_MIS", StateRejected},
		{"021", nil, fill(Template("021"), "3"), 0, "LIMIT_MIS", StateRejected},
		{"017", nil, fill(Template("017"), "1119", "10000688"), 0, "BOOKING_ERROR", StateRejected},
		{"017 suffix", nil, fill(Template("017"), "1119", "10000688") + " - для веб-записи", 0, "BOOKING_ERROR", StateRejected},
		{"002", nil, fill(Template("002")), 0, "BOOKING_ERROR", StateRejected},
		{"003", nil, fill(Template("003")), 0, "BOOKING_ERROR", StateRejected},
		{"004", nil, fill(Template("004")), 0, "BOOKING_ERROR", StateRejected},
		{"005", nil, fill(Template("005")), 0, "BOOKING_ERROR", StateRejected},
		{"006", nil, fill(Template("006")), 0, "BOOKING_ERROR", StateRejected},
		{"019", nil, fill(Template("019"), "55"), 0, "BOOKING_ERROR", StateRejected},
		{"020", nil, fill(Template("020"), "WEB"), 0, "BOOKING_ERROR", StateRejected},
		{"unmapped", &id, "что-то другое", 0, "BOOKING_UNKNOWN", StateUnknown},
		{"unmapped null id", nil, "новый текст процедуры", 0, "BOOKING_UNKNOWN", StateUnknown},
		{"candidate", &id, "", 0, "", StateCandidate},
		{"candidate spaces", &id, "   ", 0, "", StateCandidate},
		{"empty id", nil, "", 0, "BOOKING_UNKNOWN", StateUnknown},
		{"zero id", func() *int64 { z := int64(0); return &z }(), "", 0, "BOOKING_UNKNOWN", StateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.id, tc.result, tc.state)
			if got.Code != tc.code || got.State != tc.st {
				t.Fatalf("got code=%s state=%s", got.Code, got.State)
			}
			raw := strings.TrimSpace(tc.result)
			if raw != "" && (strings.Contains(got.Code, raw) || strings.Contains(Message(got.Code), raw)) {
				t.Fatal("raw result leaked into code or message")
			}
		})
	}
}

func TestLanguageOK(t *testing.T) {
	if !LanguageOK(" us_english ") || LanguageOK("русский") || LanguageOK("") {
		t.Fatal("language")
	}
}
