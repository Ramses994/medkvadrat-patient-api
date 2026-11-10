package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveJournalBusy(t *testing.T) {
	if err := liveJournalBusy("", "tok"); err == nil || !strings.Contains(err.Error(), "LK_LIVE_API_URL") {
		t.Fatalf("%v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "in_flight,unknown,partial" {
			t.Errorf("state %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer stand" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		switch {
		case strings.Contains(r.URL.Path, "/open/"):
			_, _ = w.Write([]byte(`{"success":true,"data":[{"op_id":"a","state":"unknown"}]}`))
		case strings.Contains(r.URL.Path, "/bad/"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		}
	}))
	defer srv.Close()
	if err := liveJournalBusy(srv.URL+"/empty", "stand"); err != nil {
		t.Fatal(err)
	}
	if err := liveJournalBusy(srv.URL+"/open", "stand"); err == nil {
		t.Fatal("open journal started")
	}
	if err := liveJournalBusy(srv.URL+"/bad", "stand"); err == nil {
		t.Fatal("error response started")
	}
}
