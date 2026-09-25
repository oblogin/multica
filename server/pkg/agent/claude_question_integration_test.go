//go:build agentintegration

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestClaudeRealAskUserQuestionControlProtocol verifies the installed CLI's
// control request and response shape. It is deliberately excluded from default
// tests and requires MULTICA_RUN_REAL_AGENT_SMOKE=1 before executable lookup.
func TestClaudeRealAskUserQuestionControlProtocol(t *testing.T) {
	requireRealAgentSmoke(t)
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude CLI is not installed")
	}
	backend, err := New("claude", Config{ExecutablePath: path, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	model := os.Getenv("MULTICA_REAL_AGENT_SMOKE_MODEL")
	if model == "" {
		model = "haiku"
	}
	var requested, acknowledged atomic.Int32
	session, err := backend.Execute(ctx,
		"Use AskUserQuestion to ask exactly: Which scope? Offer A and B. Wait for the user answer, then reply with exactly the selected letter.",
		ExecOptions{
			Cwd: t.TempDir(), Model: model, Timeout: 110 * time.Second,
			LiveQuestion: func(_ context.Context, requestID string, input json.RawMessage) (map[string]string, error) {
				requested.Add(1)
				if requestID == "" || !strings.Contains(string(input), "Which scope?") {
					t.Errorf("unexpected question: %q", input)
				}
				return map[string]string{"Which scope?": "A"}, nil
			},
			LiveQuestionAck: func(_ context.Context, requestID string) error {
				acknowledged.Add(1)
				if requestID == "" {
					t.Error("empty request id on acknowledgement")
				}
				return nil
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	result := <-session.Result
	if result.Status != "completed" || requested.Load() != 1 || acknowledged.Load() != 1 ||
		!strings.Contains(result.Output, "A") {
		t.Fatalf("Claude interactive protocol: status=%q request=%d ack=%d output=%q error=%q",
			result.Status, requested.Load(), acknowledged.Load(), result.Output, result.Error)
	}
}
