package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestTaskAskUnsupportedServerReportsUnsavedQuestion(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		err := taskAskSaveError(&cli.HTTPError{StatusCode: status, Method: "POST", Path: "/api/tasks/t/interactions"})
		if !strings.Contains(err.Error(), "question was not saved") {
			t.Fatalf("status %d: %v", status, err)
		}
	}
	upstream := errors.New("network down")
	if err := taskAskSaveError(upstream); !errors.Is(err, upstream) {
		t.Fatalf("lost transport error: %v", err)
	}
}
