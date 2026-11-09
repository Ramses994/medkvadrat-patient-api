package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// liveJournalTimeout bounds the stand journal check (a variable for tests).
var liveJournalTimeout = 10 * time.Second

func liveJournalBusy(baseURL, token string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return errors.New("нужны LK_LIVE_API_URL и LK_LIVE_API_TOKEN")
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/internal/write-ops?state=in_flight,unknown,partial", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: liveJournalTimeout}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	const maxBody = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(raw) > maxBody {
		return errors.New("ответ журнала стенда больше 1 МиБ")
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	// Only an explicit empty list proves the stand journal is clean:
	// {"success":true,"data":[]}. Anything else (no data, null, success:false,
	// an object) stops the experiment.
	var env struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("ответ журнала стенда не разобран: %w", err)
	}
	if env.Success == nil || !*env.Success {
		return errors.New("журнал стенда не подтвердил success:true")
	}
	data := bytes.TrimSpace(env.Data)
	if len(data) == 0 || data[0] != '[' {
		return errors.New("журнал стенда не вернул список data")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("список data не разобран: %w", err)
	}
	if len(items) > 0 {
		return errors.New("в журнале стенда есть открытые операции")
	}
	return nil
}
