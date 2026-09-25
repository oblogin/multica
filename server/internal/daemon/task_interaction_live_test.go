package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveQuestionSessionPreservesProcessAndHumanAnswer(t *testing.T) {
	var created, claimed, acked int
	var key, nonce string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProviderRequestID string `json:"provider_request_id"`
			ProcessNonce      string `json:"process_nonce"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(400)
			return
		}
		if key == "" {
			key, nonce = body.ProviderRequestID, body.ProcessNonce
		}
		if key != body.ProviderRequestID || nonce != body.ProcessNonce {
			t.Errorf("question moved to another provider request or process")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/claim"):
			claimed++
			if claimed == 1 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"interaction_id":"question-1","answer":{"q0":"A"}}`))
		case strings.HasSuffix(r.URL.Path, "/ack"):
			acked++
			_, _ = w.Write([]byte(`{}`))
		default:
			created++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"question-1"}`))
		}
	}))
	defer server.Close()
	session := newLiveQuestionSession(NewClient(server.URL), Task{ID: "task-1", RuntimeID: "runtime-1", DispatchedAt: "2026-09-25T00:00:00Z"})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	answers, err := session.resolve(ctx, "request-1", json.RawMessage(`{"questions":[{"question":"Which scope?","options":[{"label":"A"},{"label":"B"}]}],"answers":{"Which scope?":"model answer"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if answers["Which scope?"] != "A" {
		t.Fatalf("wrong human answer: %+v", answers)
	}
	if err := session.ack(ctx, "request-1"); err != nil {
		t.Fatal(err)
	}
	if created != 1 || claimed != 2 || acked != 1 {
		t.Fatalf("create=%d claim=%d ack=%d", created, claimed, acked)
	}
	if key == "" || nonce == "" || !strings.HasPrefix(key, nonce+":") {
		t.Fatalf("unbound provider key %q", key)
	}
}

func TestLiveQuestionRejectsUnsupportedClaudeInput(t *testing.T) {
	for _, raw := range []string{
		`{"questions":[{"question":"same"},{"question":"same"}]}`,
		`{"questions":[{"question":"pick","multiSelect":true}]}`,
		`{"questions":[]}`,
	} {
		if _, _, err := parseClaudeQuestions(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted unsupported input: %s", raw)
		}
	}
}
