package problem_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"tripfolio/server/internal/transport/httpapi/problem"
)

func TestWriteProducesProblemJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	problem.Write(rec, 412, "VERSION_CONFLICT", "请核对另一端的修改后重试。", "req_1")

	if rec.Code != 412 {
		t.Fatalf("status = %d, want 412", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != problem.ContentType {
		t.Fatalf("Content-Type = %q, want %q", got, problem.ContentType)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	for key, want := range map[string]any{
		"type":       "about:blank",
		"title":      "资源已被修改",
		"status":     float64(412),
		"code":       "VERSION_CONFLICT",
		"detail":     "请核对另一端的修改后重试。",
		"request_id": "req_1",
	} {
		if body[key] != want {
			t.Errorf("%s = %v, want %v", key, body[key], want)
		}
	}
	if _, present := body["errors"]; present {
		t.Error("errors should be omitted when empty")
	}
}

func TestTitleFallsBackToCode(t *testing.T) {
	if got := problem.Title("SOMETHING_NEW"); got != "SOMETHING_NEW" {
		t.Fatalf("Title = %q", got)
	}
}
