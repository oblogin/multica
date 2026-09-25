package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLiveInteractionBindsAnswerToSourceProcess(t *testing.T) {
	f := newSupplementFixture(t, "claude", "running", false)
	dbfx.Cleanup(t, `DELETE FROM inbox_item WHERE issue_id=$1 AND type='task_interaction'`, f.issueID)
	dbfx.Cleanup(t, `DELETE FROM task_interaction_audit WHERE issue_id=$1`, f.issueID)
	dbfx.Cleanup(t, `DELETE FROM task_interaction WHERE issue_id=$1`, f.issueID)
	dbfx.Cleanup(t, `DELETE FROM task_interaction_capability WHERE task_id=$1`, f.taskID)
	generation := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	dbfx.Exec(t, `UPDATE agent_task_queue SET dispatched_at=$2 WHERE id=$1`, f.taskID, generation)
	dbfx.Exec(t, `INSERT INTO task_interaction_capability
		(task_id,workspace_id,issue_id,runtime_id,claim_generation,capability,live_enabled)
		VALUES($1,$2,$3,$4,$5,$6,true)`, f.taskID, testWorkspaceID, f.issueID,
		f.runtimeID, generation, protocol.DaemonCapabilityTaskInteractionLiveV1)
	oldFlag := testHandler.cfg.LiveInteractions
	testHandler.cfg.LiveInteractions = true
	t.Cleanup(func() { testHandler.cfg.LiveInteractions = oldFlag })
	processNonce := uuid.NewString()
	requestID := "claude-request-1"
	body := func(nonce, runtime, claim string) map[string]any {
		return map[string]any{
			"runtime_id": runtime, "dispatched_at": claim, "process_nonce": nonce,
			"provider_request_id": requestID,
			"questions":           []map[string]any{{"id": "q0", "question": "Which scope?", "options": []string{"A", "B"}}},
		}
	}
	call := func(method func(http.ResponseWriter, *http.Request), interactionID string, payload map[string]any) *testutil.Response {
		req := newDaemonTokenRequest(http.MethodPost, "/live", payload, testWorkspaceID, "live-test")
		req = withURLParams(req, "taskId", f.taskID, "interactionId", interactionID)
		return testutil.Call(t, method, req)
	}
	valid := body(processNonce, f.runtimeID, generation.Format(time.RFC3339Nano))
	call(testHandler.CreateLiveTaskInteraction, "", body(uuid.NewString(), uuid.NewString(), generation.Format(time.RFC3339Nano))).Want(http.StatusConflict)
	call(testHandler.CreateLiveTaskInteraction, "", body(uuid.NewString(), f.runtimeID, generation.Add(time.Second).Format(time.RFC3339Nano))).Want(http.StatusConflict)
	var created map[string]any
	call(testHandler.CreateLiveTaskInteraction, "", valid).Want(http.StatusCreated).JSON(&created)
	interactionID := created["id"].(string)
	call(testHandler.CreateLiveTaskInteraction, "", valid).Want(http.StatusOK)
	call(testHandler.CreateLiveTaskInteraction, "", body(uuid.NewString(), f.runtimeID, generation.Format(time.RFC3339Nano))).Want(http.StatusConflict)
	if n := dbfx.Count(t, `SELECT count(*) FROM task_interaction WHERE task_id=$1`, f.taskID); n != 1 {
		t.Fatalf("replayed provider request created %d interactions", n)
	}
	delete(valid, "questions")
	call(testHandler.ClaimLiveTaskInteraction, interactionID, valid).Want(http.StatusNoContent)
	wrongProcess := body(uuid.NewString(), f.runtimeID, generation.Format(time.RFC3339Nano))
	delete(wrongProcess, "questions")
	call(testHandler.ClaimLiveTaskInteraction, interactionID, wrongProcess).Want(http.StatusNotFound)
	answerReq := newRequest(http.MethodPost, "/answer", map[string]any{
		"expected_version": 1, "client_request_id": uuid.NewString(), "answer": map[string]string{"q0": "A"},
	})
	answerReq = withURLParams(answerReq, "id", f.issueID, "taskId", f.taskID, "interactionId", interactionID)
	claimReq := withURLParams(newDaemonTokenRequest(http.MethodPost, "/claim", valid, testWorkspaceID, "live-test"),
		"taskId", f.taskID, "interactionId", interactionID)
	start := make(chan struct{})
	answerStatus, claimStatus := make(chan int, 1), make(chan int, 1)
	go func() {
		<-start
		w := httptest.NewRecorder()
		testHandler.AnswerTaskInteraction(w, answerReq)
		answerStatus <- w.Code
	}()
	go func() {
		<-start
		w := httptest.NewRecorder()
		testHandler.ClaimLiveTaskInteraction(w, claimReq)
		claimStatus <- w.Code
	}()
	close(start)
	if status := <-answerStatus; status != http.StatusOK {
		t.Fatalf("racing answer: %d", status)
	}
	if status := <-claimStatus; status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("racing claim: %d", status)
	}
	var claimed struct {
		Answer map[string]string `json:"answer"`
	}
	call(testHandler.ClaimLiveTaskInteraction, interactionID, valid).Want(http.StatusOK).JSON(&claimed)
	if claimed.Answer["q0"] != "A" {
		t.Fatalf("wrong live answer: %+v", claimed.Answer)
	}
	call(testHandler.AckLiveTaskInteraction, interactionID, wrongProcess).Want(http.StatusNotFound)
	call(testHandler.AckLiveTaskInteraction, interactionID, valid).Want(http.StatusOK)
	call(testHandler.ClaimLiveTaskInteraction, interactionID, valid).Want(http.StatusConflict)
	if w := completeTaskViaHandler(t, f.taskID, "answered in source run"); w.Code != http.StatusOK {
		t.Fatalf("complete source task: %d %s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(t.Context(), `SELECT status FROM task_interaction WHERE id=$1`, interactionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "settled" {
		t.Fatalf("delivered answer became %q after source completion", status)
	}
}

func TestLiveInteractionCapabilityRequiresFeatureAndClaim(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			f := newSupplementFixture(t, "claude", "dispatched", false)
			dbfx.Cleanup(t, `DELETE FROM task_interaction_capability WHERE task_id=$1`, f.taskID)
			generation := time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC)
			dbfx.Exec(t, `UPDATE agent_task_queue SET dispatched_at=$2 WHERE id=$1`, f.taskID, generation)
			dbfx.Exec(t, `INSERT INTO task_interaction_capability
			(task_id,workspace_id,issue_id,runtime_id,claim_generation,capability)
			VALUES($1,$2,$3,$4,$5,$6)`, f.taskID, testWorkspaceID, f.issueID,
				f.runtimeID, generation, protocol.DaemonCapabilityTaskInteractionContextV1)
			oldFlag := testHandler.cfg.LiveInteractions
			testHandler.cfg.LiveInteractions = enabled
			request := newDaemonTokenRequest(http.MethodPost, "/start", map[string]any{
				"runtime_id": f.runtimeID, "dispatched_at": generation.Format(time.RFC3339Nano),
				"capabilities": []string{protocol.DaemonCapabilityTaskInteractionLiveV1},
			}, testWorkspaceID, "live-test")
			var response AgentTaskResponse
			testutil.Call(t, testHandler.StartTask, withURLParam(request, "taskId", f.taskID)).Want(http.StatusOK).JSON(&response)
			testHandler.cfg.LiveInteractions = oldFlag
			if got := response.InteractionLiveCapability == protocol.DaemonCapabilityTaskInteractionLiveV1; got != enabled {
				t.Fatalf("feature enabled=%v, negotiated=%v", enabled, got)
			}
		})
	}
}
