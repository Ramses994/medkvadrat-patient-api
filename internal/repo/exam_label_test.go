package repo

import (
	"strings"
	"testing"
)

func TestCleanServiceName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Прием врача-гастроэнтеролога первичный 30 мин(р)", "Прием врача-гастроэнтеролога первичный"},
		{"УЗИ органов брюшной полости (р) - 5100 руб.", "УЗИ органов брюшной полости"},
		{"Консультация", "Консультация"},
	}
	for _, tc := range cases {
		if got := CleanServiceName(tc.in); got != tc.want {
			t.Errorf("CleanServiceName(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestDisplayServiceLanguageTags(t *testing.T) {
	cases := []struct {
		web, name, want string
	}{
		{"#rus#Прием врачом-терапевтом", "ignored", "Прием врачом-терапевтом"},
		{"#rus#Обзорная рентгенография органов грудной клетки#eng#Chest X-ray", "ignored", "Обзорная рентгенография органов грудной клетки"},
		{"#rus#", "Прием врача 30 мин(р)", "Прием врача"},
		{"Прием терапевта 30 мин(р)", "other", "Прием терапевта"},
		{"", "УЗИ (р)", "УЗИ"},
		{"#eng#Chest X-ray", "NAME", "Chest X-ray"},
	}
	for _, tc := range cases {
		if got := DisplayService(tc.web, tc.name); got != tc.want {
			t.Errorf("DisplayService(%q,%q)=%q want %q", tc.web, tc.name, got, tc.want)
		}
	}
}

func TestIsWaitlistExamByNameOrID(t *testing.T) {
	if !IsWaitlistExam("Лист ожидания 10 мин", 1, nil) {
		t.Fatal("NAME with ожидан must be waitlist")
	}
	if IsWaitlistExam("Прием терапевта", 1, nil) {
		t.Fatal("normal exam must not be waitlist")
	}
	if !IsWaitlistExam("Прием терапевта", 3118, nil) {
		t.Fatal("default exam id 3118 must be waitlist even with normal WEB_NAME/NAME")
	}
	if !IsWaitlistExam("Прием терапевта", 9999, []int64{9999}) {
		t.Fatal("configured exam id must be waitlist")
	}
}

func TestExamSelectSQLWaitlistRule(t *testing.T) {
	sql, args := examSelectSQL(nil)
	if !strings.Contains(sql, `LIKE N'%ожидан%'`) {
		t.Fatalf("sql missing NAME LIKE: %s", sql)
	}
	if !strings.Contains(sql, "PL_EXAM_ID IN") {
		t.Fatalf("sql missing exam id list: %s", sql)
	}
	if !strings.Contains(sql, "EXAM_WEB_NAME") || !strings.Contains(sql, "EXAM_NAME") {
		t.Fatalf("sql must return WEB_NAME and NAME separately: %s", sql)
	}
	if len(args) != 1 {
		t.Fatalf("default waitlist ids args=%d", len(args))
	}
}
