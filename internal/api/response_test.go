package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOK_Format(t *testing.T) {
	rr := httptest.NewRecorder()
	OK(rr, map[string]string{"x": "y"})

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["success"] != true {
		t.Fatalf("success=%v", got["success"])
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("data type=%T", got["data"])
	}
	if data["x"] != "y" {
		t.Fatalf("data.x=%v", data["x"])
	}
}

func TestError_Format(t *testing.T) {
	rr := httptest.NewRecorder()
	Error(rr, http.StatusBadRequest, "VALIDATION", "bad")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["success"] != false {
		t.Fatalf("success=%v", got["success"])
	}
	if got["error"] != "bad" {
		t.Fatalf("error=%v", got["error"])
	}

	ed, ok := got["error_details"].(map[string]any)
	if !ok {
		t.Fatalf("error_details type=%T", got["error_details"])
	}
	if ed["code"] != "VALIDATION" || ed["message"] != "bad" {
		t.Fatalf("error_details=%v", ed)
	}
}
