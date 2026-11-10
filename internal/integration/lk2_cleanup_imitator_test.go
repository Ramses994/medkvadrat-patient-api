package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type errCapture struct {
	testing.TB
	msgs []string
}

func (c *errCapture) Errorf(format string, args ...any) {
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *errCapture) Helper() {}

func TestCleanupContinuesAfterTransportError(t *testing.T) {
	var cancelled []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path, "/api/me/appointments/%d", &id)
		cancelled = append(cancelled, id)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/11") {
			return nil, errors.New("transport down")
		}
		return http.DefaultTransport.RoundTrip(r)
	})

	var ids idList
	raw := []byte(`{"data":{"planning_id":11}}`)
	var parsed struct {
		Data struct {
			PlanningID int64 `json:"planning_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	notePlanningID(&ids, parsed.Data.PlanningID)
	notePlanningID(&ids, 12)
	notePlanningID(&ids, 13)
	got := ids.snapshot()
	if len(got) != 3 || got[0] != 11 {
		t.Fatalf("registered %v", got)
	}

	cap := &errCapture{TB: t}
	cancelEach(cap, got, nil, func(id int64, key string) error {
		return cancelAppointment(client, srv.URL, "token", id, key)
	}, nil)
	if len(cancelled) != 2 || cancelled[0] != 12 || cancelled[1] != 13 {
		t.Fatalf("cancelled %v", cancelled)
	}
	text := strings.Join(cap.msgs, "\n")
	if !strings.Contains(text, "planning_id=11") || !strings.Contains(text, "не отменены planning_id:") || !strings.Contains(text, "отменить в клиенте") {
		t.Fatalf("report %s", text)
	}
	t.Log(text)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCleanup_ParallelRegister(t *testing.T) {
	var ids idList
	var wg sync.WaitGroup
	for i := int64(1); i <= 40; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			notePlanningID(&ids, id)
		}(i)
	}
	wg.Wait()
	got := ids.snapshot()
	if len(got) != 40 {
		t.Fatalf("len %d", len(got))
	}
}

func TestCleanup_RetrySameKeyConfirms(t *testing.T) {
	var mu sync.Mutex
	keys := map[int64][]string{}
	hits := map[int64]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path, "/api/me/appointments/%d", &id)
		mu.Lock()
		hits[id]++
		n := hits[id]
		keys[id] = append(keys[id], r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if id == 21 && n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"success":false,"error_details":{"code":"CANCEL_UNKNOWN"}}`))
			return
		}
		if id == 21 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"success":false,"error_details":{"code":"ALREADY_CANCELLED"}}`))
			return
		}
		if id == 22 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"success":false,"error_details":{"code":"CANCEL_NOT_SUPPORTED"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer srv.Close()
	client := srv.Client()
	cap := &errCapture{TB: t}
	cancelEach(cap, []int64{21, 22}, nil, func(id int64, key string) error {
		return cancelAppointment(client, srv.URL, "token", id, key)
	}, func(id int64) (bool, error) {
		return id == 21 || id == 22, nil
	})
	if len(cap.msgs) != 0 {
		t.Fatalf("uncancelled %s", strings.Join(cap.msgs, "\n"))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys[21]) != 2 || keys[21][0] == "" || keys[21][0] != keys[21][1] {
		t.Fatalf("keys %v", keys[21])
	}
	if hits[22] < 2 {
		t.Fatalf("hits %v", hits)
	}
}
