package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// cursor-lk-2-fixes-4.md, item 5: only {"success":true,"data":[]} lets B7 start.
func TestLiveJournalBusy_StrictEmptyList(t *testing.T) {
	bodies := map[string]string{
		"/obj/":     `{}`,
		"/null/":    `{"success":true,"data":null}`,
		"/nodata/":  `{"success":true}`,
		"/false/":   `{"success":false,"data":[]}`,
		"/object/":  `{"success":true,"data":{}}`,
		"/garbage/": `not json`,
		"/empty/":   `{"success":true,"data":[]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, body := range bodies {
			if strings.HasPrefix(r.URL.Path, prefix) {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/slow/") {
			time.Sleep(500 * time.Millisecond)
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	for prefix := range bodies {
		err := liveJournalBusy(srv.URL+strings.TrimSuffix(prefix, "/"), "stand")
		if prefix == "/empty/" {
			if err != nil {
				t.Fatalf("empty list must start: %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s must not start", prefix)
		}
	}
	old := liveJournalTimeout
	liveJournalTimeout = 100 * time.Millisecond
	defer func() { liveJournalTimeout = old }()
	if err := liveJournalBusy(srv.URL+"/slow", "stand"); err == nil {
		t.Fatal("timeout must not start")
	}
}

// cursor-lk-2-fixes-4.md, item 6: the scenario's first DELETE loses its response;
// the cleanup retries with the same key, so the server sees one intent and the
// procedure runs once.
func TestCleanup_LostFirstDeleteReusesScenarioKey(t *testing.T) {
	var mu sync.Mutex
	keys := map[int64][]string{}
	procByKey := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path, "/api/me/appointments/%d", &id)
		key := r.Header.Get("Idempotency-Key")
		mu.Lock()
		keys[id] = append(keys[id], key)
		first := len(keys[id]) == 1
		if procByKey[key] == 0 {
			procByKey[key]++ // the API runs the procedure once per key, replays after
		}
		mu.Unlock()
		if first {
			// Processed, but the response is lost.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"planning_id":31}}`))
	}))
	defer srv.Close()
	client := srv.Client()
	var ids idList
	ids.add(31)
	// Scenario cancel: the response is lost.
	if err := cancelAppointment(client, srv.URL, "token", 31, ids.cancelKey(31)); err == nil {
		t.Fatal("lost response must surface as an error")
	}
	// Cleanup: same key, confirmed.
	cap := &errCapture{TB: t}
	cancelEach(cap, ids.snapshot(), ids.cancelKey, func(id int64, key string) error {
		return cancelAppointment(client, srv.URL, "token", id, key)
	}, func(int64) (bool, error) { return true, nil })
	if len(cap.msgs) != 0 {
		t.Fatalf("cleanup failed: %s", strings.Join(cap.msgs, "\n"))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys[31]) < 2 {
		t.Fatalf("requests %v", keys[31])
	}
	for _, k := range keys[31] {
		if k != keys[31][0] {
			t.Fatalf("key changed between scenario and cleanup: %v", keys[31])
		}
	}
	if len(procByKey) != 1 {
		t.Fatalf("intents %v", procByKey)
	}
}
