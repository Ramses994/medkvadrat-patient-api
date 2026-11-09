package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

	var ids []int64
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
	if len(ids) != 3 || ids[0] != 11 {
		t.Fatalf("registered %v", ids)
	}

	cap := &errCapture{TB: t}
	cancelEach(cap, ids, func(id int64) error {
		return cancelAppointment(client, srv.URL, "token", id)
	})
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
