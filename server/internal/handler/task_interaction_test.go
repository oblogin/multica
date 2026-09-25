package handler

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestInteractionPayloadValidation(t *testing.T) {
	questions := []interactionQuestion{{ID: "scope", Question: "Which scope?", Options: []string{"A", "B"}}}
	if !validateInteractionQuestions(questions) {
		t.Fatal("valid choice rejected")
	}
	if validateInteractionQuestions([]interactionQuestion{{ID: "x", Question: "one"}, {ID: "x", Question: "two"}}) {
		t.Fatal("duplicate question ids accepted")
	}
	if validateInteractionQuestions([]interactionQuestion{{ID: "x", Question: " "}}) {
		t.Fatal("empty question accepted")
	}
	if validateInteractionAnswer([]byte(`[{"id":"scope","question":"Which scope?","options":["A","B"]}]`), map[string]string{"scope": "C"}) {
		t.Fatal("unknown choice accepted")
	}
	if !validateInteractionAnswer([]byte(`[{"id":"scope","question":"Which scope?","options":["A","B"]}]`), map[string]string{"scope": "B"}) {
		t.Fatal("valid answer rejected")
	}
}

func TestDetachedInteractionOwnTaskAndConcurrentAnswer(t *testing.T) {
	f := newSupplementFixture(t, "codex", "running", false)
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE issue_id=$1 AND type='task_interaction'`, f.issueID)
	dbfx.Cleanup(t, `DELETE FROM task_interaction_audit WHERE issue_id=$1`, f.issueID)
	dbfx.Cleanup(t, `DELETE FROM task_interaction WHERE issue_id=$1`, f.issueID)
	requestID := uuid.NewString()
	create := func(taskHeader string) *testutil.Response {
		req := newRequest(http.MethodPost, "/api/tasks/"+f.taskID+"/interactions", map[string]any{
			"client_request_id": requestID,
			"questions":         []map[string]any{{"id": "answer", "question": "Which scope?"}},
		})
		req.Header.Set("X-Actor-Source", "task_token")
		req.Header.Set("X-Task-ID", taskHeader)
		req.Header.Set("X-Agent-ID", f.agentID)
		return testutil.Call(t, testHandler.CreateDetachedInteraction, withURLParam(req, "taskId", f.taskID))
	}
	create(uuid.NewString()).Want(http.StatusForbidden)
	var created map[string]any
	create(f.taskID).Want(http.StatusCreated).JSON(&created)
	create(f.taskID).Want(http.StatusOK)
	if count := dbfx.Count(t, `SELECT count(*) FROM task_interaction WHERE task_id=$1`, f.taskID); count != 1 {
		t.Fatalf("idempotent create wrote %d questions", count)
	}
	interactionID := created["id"].(string)
	answer := func(requestID, text string) int {
		req := newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/tasks/"+f.taskID+"/interactions/"+interactionID+"/answer", map[string]any{
			"expected_version": 1, "client_request_id": requestID, "answer": map[string]string{"answer": text},
		})
		req = withURLParams(req, "id", f.issueID, "taskId", f.taskID, "interactionId", interactionID)
		w := httptest.NewRecorder()
		testHandler.AnswerTaskInteraction(w, req)
		return w.Code
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for _, value := range []string{"A", "B"} {
		wg.Add(1)
		go func(value string) { defer wg.Done(); statuses <- answer(uuid.NewString(), value) }(value)
	}
	wg.Wait()
	close(statuses)
	accepted, conflicts := 0, 0
	for status := range statuses {
		if status == http.StatusOK {
			accepted++
		}
		if status == http.StatusConflict {
			conflicts++
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("answer race: accepted=%d conflicts=%d", accepted, conflicts)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM task_interaction_audit WHERE interaction_id=$1 AND action='answered'`, interactionID); count != 1 {
		t.Fatalf("answer race wrote %d audit entries", count)
	}
}
