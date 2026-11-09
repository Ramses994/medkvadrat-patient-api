package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

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
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	var env struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if len(env.Data) > 0 {
		return errors.New("в журнале стенда есть открытые операции")
	}
	return nil
}
