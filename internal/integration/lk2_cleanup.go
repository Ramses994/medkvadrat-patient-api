package integration

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func notePlanningID(ids *[]int64, id int64) {
	if id > 0 {
		*ids = append(*ids, id)
	}
}

func cancelEach(t testing.TB, ids []int64, cancel func(int64) error) {
	t.Helper()
	seen := map[int64]bool{}
	var failed []int64
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if err := cancel(id); err != nil {
			failed = append(failed, id)
			t.Errorf("planning_id=%d %v. отменить в клиенте: Расписание → линия 1119 → запись → „Пациент не придёт“", id, err)
		}
	}
	if len(failed) > 0 {
		t.Errorf("не отменены planning_id: %v", failed)
	}
}

func cancelAppointment(client *http.Client, baseURL, bearer string, id int64) error {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/me/appointments/%d", baseURL, id), nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Idempotency-Key", cleanupKey())
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	var env struct {
		ErrorDetails struct {
			Code string `json:"code"`
		} `json:"error_details"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("status=%d", res.StatusCode)
	}
	if res.StatusCode == http.StatusOK || env.ErrorDetails.Code == "ALREADY_CANCELLED" {
		return nil
	}
	return fmt.Errorf("status=%d code=%s", res.StatusCode, env.ErrorDetails.Code)
}

func cleanupKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
