package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type liveInteractionRequest struct {
	RuntimeID         string                `json:"runtime_id"`
	DispatchedAt      string                `json:"dispatched_at"`
	ProcessNonce      string                `json:"process_nonce"`
	ProviderRequestID string                `json:"provider_request_id"`
	Questions         []interactionQuestion `json:"questions,omitempty"`
}

func (h *Handler) liveInteractionClaim(w http.ResponseWriter, r *http.Request, create bool) (liveInteractionRequest, pgtype.UUID, pgtype.UUID, pgtype.UUID, pgtype.UUID, time.Time, bool) {
	var req liveInteractionRequest
	if !h.cfg.LiveInteractions {
		writeErrorCode(w, http.StatusPreconditionFailed, "live_interaction_disabled", "live interaction support is disabled")
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	task, workspace, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	if !task.IssueID.Valid {
		writeErrorCode(w, http.StatusPreconditionFailed, "issue_task_required", "live questions require an issue task")
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	if err := decodeInteractionBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid interaction request")
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	runtimeID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	processNonce, ok := parseUUIDOrBadRequest(w, req.ProcessNonce, "process_nonce")
	if !ok {
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	generation, err := time.Parse(time.RFC3339Nano, req.DispatchedAt)
	if err != nil || generation.Nanosecond()%1000 != 0 || runtimeID != task.RuntimeID || !task.DispatchedAt.Valid || !generation.Equal(task.DispatchedAt.Time) {
		writeErrorCode(w, http.StatusConflict, "stale_claim", "the task belongs to another runtime or claim")
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	if strings.TrimSpace(req.ProviderRequestID) == "" || len(req.ProviderRequestID) > 200 || (create && !validateInteractionQuestions(req.Questions)) {
		writeError(w, http.StatusBadRequest, "invalid provider request or questions")
		return req, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Time{}, false
	}
	return req, parseUUID(workspace), task.ID, task.IssueID, processNonce, generation, true
}

// CreateLiveTaskInteraction persists a Claude question for the exact daemon
// process and claim. A task token cannot reach this daemon-authenticated route.
func (h *Handler) CreateLiveTaskInteraction(w http.ResponseWriter, r *http.Request) {
	req, workspaceID, taskID, issueID, processNonce, generation, ok := h.liveInteractionClaim(w, r, true)
	if !ok {
		return
	}
	questions, _ := json.Marshal(req.Questions)
	var agentID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `SELECT agent_id FROM agent_task_queue WHERE id=$1`, taskID).Scan(&agentID); err != nil {
		writeError(w, 500, "failed to load task agent")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to create live question")
		return
	}
	defer tx.Rollback(r.Context())
	if err := lockInteractionScope(r.Context(), tx, workspaceID, issueID, agentID); err != nil {
		writeError(w, 500, "failed to create live question")
		return
	}
	var status string
	var threadID pgtype.UUID
	if err := tx.QueryRow(r.Context(), `SELECT status,comment_thread_id FROM agent_task_queue
		WHERE id=$1 AND issue_id=$2 AND agent_id=$3 AND runtime_id=$4 AND dispatched_at=$5 FOR UPDATE`,
		taskID, issueID, agentID, parseUUID(req.RuntimeID), generation).Scan(&status, &threadID); err != nil || status != "running" {
		writeErrorCode(w, 409, "stale_claim", "the source process is no longer running")
		return
	}
	var enabled bool
	if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM task_interaction_capability
		WHERE task_id=$1 AND runtime_id=$2 AND claim_generation=$3 AND live_enabled=true)`,
		taskID, parseUUID(req.RuntimeID), generation).Scan(&enabled); err != nil || !enabled {
		writeErrorCode(w, 412, "live_capability_required", "this run did not negotiate live questions")
		return
	}
	var v interactionRecord
	v, err = scanInteraction(tx.QueryRow(r.Context(), `SELECT `+interactionColumns+` FROM task_interaction
		WHERE task_id=$1 AND claim_generation=$2 AND provider_request_id=$3`, taskID, generation, req.ProviderRequestID))
	if err == nil {
		var saved []interactionQuestion
		var savedNonce pgtype.UUID
		if scanErr := tx.QueryRow(r.Context(), `SELECT process_nonce FROM task_interaction WHERE id=$1`, v.ID).Scan(&savedNonce); scanErr != nil ||
			json.Unmarshal(v.Questions, &saved) != nil || !reflect.DeepEqual(saved, req.Questions) || savedNonce != processNonce {
			writeErrorCode(w, 409, "idempotency_conflict", "provider request was used for another question or process")
			return
		}
		writeJSON(w, 200, interactionJSON(v, false))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "failed to create live question")
		return
	}
	v, err = scanInteraction(tx.QueryRow(r.Context(), `INSERT INTO task_interaction
		(id,workspace_id,issue_id,agent_id,comment_thread_id,task_id,runtime_id,claim_generation,
		 provider,provider_request_id,process_nonce,mode,questions,status,expires_at,detached_expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'claude',$9,$10,'live',$11,'pending',now()+interval '30 minutes',now()+interval '7 days')
		RETURNING `+interactionColumns, dbid.NewV7(), workspaceID, issueID, agentID, threadID, taskID, parseUUID(req.RuntimeID), generation, req.ProviderRequestID, processNonce, questions))
	if err != nil {
		writeErrorCode(w, 409, "interaction_conflict", "a live question is already pending")
		return
	}
	if err := appendInteractionAudit(r.Context(), tx, v, "daemon", parseUUID(req.RuntimeID), "created"); err != nil {
		writeError(w, 500, "failed to audit question")
		return
	}
	var inboxID pgtype.UUID
	err = tx.QueryRow(r.Context(), `INSERT INTO inbox_item
		(id,workspace_id,recipient_type,recipient_id,type,severity,issue_id,title,body)
		SELECT $1,$2,'member',recipient.user_id,'task_interaction','action_required',$3,
		'Agent needs input','Open this issue to answer the question.'
		FROM agent_task_queue t JOIN agent a ON a.id=t.agent_id
		JOIN LATERAL (SELECT m.user_id FROM member m WHERE m.workspace_id=$2
		AND m.user_id IN (t.accountable_user_id,a.owner_id)
		ORDER BY (m.user_id=t.accountable_user_id) DESC LIMIT 1) recipient ON true
		WHERE t.id=$4 RETURNING id`, dbid.NewV7(), workspaceID, issueID, taskID).Scan(&inboxID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "failed to notify responsible member")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to create live question")
		return
	}
	h.publish("task:interaction_changed", uuidToString(workspaceID), "agent", uuidToString(agentID), map[string]any{"issue_id": uuidToString(issueID), "task_id": uuidToString(taskID), "interaction_id": uuidToString(v.ID)})
	if inboxID.Valid {
		h.publish(protocol.EventInboxNew, uuidToString(workspaceID), "agent", uuidToString(agentID), map[string]any{"inbox_item_id": uuidToString(inboxID), "issue_id": uuidToString(issueID)})
	}
	writeJSON(w, http.StatusCreated, interactionJSON(v, false))
}

// ClaimLiveTaskInteraction returns an answer only to the process that created
// the question. A pending question returns 204 so the daemon can poll.
func (h *Handler) ClaimLiveTaskInteraction(w http.ResponseWriter, r *http.Request) {
	h.liveInteractionDelivery(w, r, false)
}

func (h *Handler) AckLiveTaskInteraction(w http.ResponseWriter, r *http.Request) {
	h.liveInteractionDelivery(w, r, true)
}

func (h *Handler) liveInteractionDelivery(w http.ResponseWriter, r *http.Request, ack bool) {
	req, workspaceID, taskID, issueID, processNonce, generation, ok := h.liveInteractionClaim(w, r, false)
	if !ok {
		return
	}
	interactionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "interactionId"), "interaction_id")
	if !ok {
		return
	}
	var agentID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `SELECT agent_id FROM agent_task_queue WHERE id=$1`, taskID).Scan(&agentID); err != nil {
		writeError(w, 500, "failed to load task agent")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to deliver answer")
		return
	}
	defer tx.Rollback(r.Context())
	if err := lockInteractionScope(r.Context(), tx, workspaceID, issueID, agentID); err != nil {
		writeError(w, 500, "failed to deliver answer")
		return
	}
	var taskStatus string
	if err := tx.QueryRow(r.Context(), `SELECT status FROM agent_task_queue WHERE id=$1 AND issue_id=$2 AND agent_id=$3 AND runtime_id=$4 AND dispatched_at=$5 FOR UPDATE`,
		taskID, issueID, agentID, parseUUID(req.RuntimeID), generation).Scan(&taskStatus); err != nil || taskStatus != "running" {
		writeErrorCode(w, 409, "stale_claim", "source process is no longer running")
		return
	}
	v, err := scanInteraction(tx.QueryRow(r.Context(), `SELECT `+interactionColumns+` FROM task_interaction
		WHERE id=$1 AND workspace_id=$2 AND task_id=$3 AND runtime_id=$4 AND claim_generation=$5 AND provider_request_id=$6 AND process_nonce=$7 FOR UPDATE`,
		interactionID, workspaceID, taskID, parseUUID(req.RuntimeID), generation, req.ProviderRequestID, processNonce))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "question not found for this process")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to deliver answer")
		return
	}
	if !ack {
		if v.Status == "pending" && time.Now().Before(v.ExpiresAt) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if v.Status == "answered" && time.Now().Before(v.ExpiresAt) {
			v, err = scanInteraction(tx.QueryRow(r.Context(), `UPDATE task_interaction SET status='delivering',version=version+1,updated_at=now()
				WHERE id=$1 AND version=$2 RETURNING `+interactionColumns, v.ID, v.Version))
			if err != nil {
				writeError(w, 500, "failed to claim answer")
				return
			}
			if err := appendInteractionAudit(r.Context(), tx, v, "daemon", parseUUID(req.RuntimeID), "delivery_claimed"); err != nil {
				writeError(w, 500, "failed to audit delivery")
				return
			}
		}
		if v.Status != "delivering" {
			writeErrorCode(w, 409, "interaction_conflict", "question is no longer deliverable")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, 500, "failed to claim answer")
			return
		}
		writeJSON(w, 200, map[string]any{"interaction_id": uuidToString(v.ID), "answer": json.RawMessage(v.Answer)})
		return
	}
	if v.Status == "delivered" {
		writeJSON(w, 200, interactionJSON(v, false))
		return
	}
	if v.Status != "delivering" {
		writeErrorCode(w, 409, "interaction_conflict", "answer was not claimed for delivery")
		return
	}
	v, err = scanInteraction(tx.QueryRow(r.Context(), `UPDATE task_interaction SET status='delivered',delivered_at=now(),version=version+1,updated_at=now()
		WHERE id=$1 AND version=$2 RETURNING `+interactionColumns, v.ID, v.Version))
	if err != nil {
		writeError(w, 500, "failed to acknowledge answer")
		return
	}
	if err := appendInteractionAudit(r.Context(), tx, v, "daemon", parseUUID(req.RuntimeID), "delivery_written"); err != nil {
		writeError(w, 500, "failed to audit delivery")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to acknowledge answer")
		return
	}
	writeJSON(w, 200, interactionJSON(v, false))
}
