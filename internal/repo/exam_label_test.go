package repo

import "testing"

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

func TestIsWaitlistService(t *testing.T) {
	if !IsWaitlistService("Лист ожидания") {
		t.Fatal("expected waitlist")
	}
	if IsWaitlistService("Прием терапевта") {
		t.Fatal("not waitlist")
	}
}
