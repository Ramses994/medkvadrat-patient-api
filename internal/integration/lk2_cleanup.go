package integration

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
)

var (
	errCancelAlready     = errors.New("already cancelled")
	errCancelUnsupported = errors.New("cancel not supported")
)

type idList struct {
	mu   sync.Mutex
	ids  []int64
	keys map[int64]string // one cancel Idempotency-Key per planning_id
}

// cancelKey returns the single cancel key for this planning_id, creating and
// storing it before the first DELETE. The scenario and the cleanup both use it,
// so a lost response is always retried as the same intent, never a new one.
func (l *idList) cancelKey(id int64) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.keys == nil {
		l.keys = map[int64]string{}
	}
	if k, ok := l.keys[id]; ok {
		return k
	}
	k := cleanupKey()
	l.keys[id] = k
	return k
}

func (l *idList) add(id int64) {
	if id <= 0 {
		return
	}
	l.mu.Lock()
	l.ids = append(l.ids, id)
	l.mu.Unlock()
}

func (l *idList) snapshot() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]int64, len(l.ids))
	copy(out, l.ids)
	return out
}

func notePlanningID(list *idList, id int64) {
	list.add(id)
}

// cancelEach cancels every id with its stored key (keyFor; nil = one fresh key per
// id), retries once with the same key, and confirms "already cancelled" by reading.
func cancelEach(t testing.TB, ids []int64, keyFor func(int64) string, cancel func(int64, string) error, confirm func(int64) (bool, error)) {
	t.Helper()
	seen := map[int64]bool{}
	var failed []int64
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		key := ""
		if keyFor != nil {
			key = keyFor(id)
		} else {
			key = cleanupKey()
		}
		err := cancel(id, key)
		if err == nil {
			continue
		}
		retry := cancel(id, key)
		if retry == nil {
			continue
		}
		if errors.Is(retry, errCancelAlready) || errors.Is(retry, errCancelUnsupported) {
			if confirm != nil {
				ok, verr := confirm(id)
				if verr == nil && ok {
					continue
				}
			}
		}
		failed = append(failed, id)
		t.Errorf("planning_id=%d %v. отменить в клиенте: Расписание → линия 1119 → запись → „Пациент не придёт“", id, retry)
	}
	if len(failed) > 0 {
		t.Errorf("не отменены planning_id: %v", failed)
	}
}

func cancelAppointment(client *http.Client, baseURL, bearer string, id int64, key string) error {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/me/appointments/%d", baseURL, id), nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if key == "" {
		key = cleanupKey()
	}
	req.Header.Set("Idempotency-Key", key)
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
	if res.StatusCode == http.StatusOK {
		return nil
	}
	if env.ErrorDetails.Code == "ALREADY_CANCELLED" {
		return errCancelAlready
	}
	if env.ErrorDetails.Code == "CANCEL_NOT_SUPPORTED" {
		return errCancelUnsupported
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
