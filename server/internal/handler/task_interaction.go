package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	interactionDetachedTTL = 7 * 24 * time.Hour
	interactionLiveTTL     = 30 * time.Minute
	interactionMaxBody     = 32 << 10
)

type interactionQuestion struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

type interactionRecord struct {
	ID                pgtype.UUID
	WorkspaceID       pgtype.UUID
	IssueID           pgtype.UUID
	AgentID           pgtype.UUID
	ThreadID          pgtype.UUID
	TaskID            pgtype.UUID
	Mode              string
	Questions         []byte
	Status            string
	Reason            pgtype.Text
	Answer            []byte
	AnsweredBy        pgtype.UUID
	AnsweredAt        pgtype.Timestamptz
	ExpiresAt         time.Time
	DetachedExpiresAt time.Time
	Version           int
	ConsumedByTaskID  pgtype.UUID
	AssignCount       int
}

func scanInteraction(row pgx.Row) (interactionRecord, error) {
	var v interactionRecord
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.IssueID, &v.AgentID, &v.ThreadID, &v.TaskID,
		&v.Mode, &v.Questions, &v.Status, &v.Reason, &v.Answer, &v.AnsweredBy,
		&v.AnsweredAt, &v.ExpiresAt, &v.DetachedExpiresAt, &v.Version,
		&v.ConsumedByTaskID, &v.AssignCount)
	return v, err
}

const interactionColumns = `id, workspace_id, issue_id, agent_id, comment_thread_id, task_id,
mode, questions, status, reason, answer, answered_by, answered_at, expires_at,
detached_expires_at, version, consumed_by_task_id, assign_count`

func interactionJSON(v interactionRecord, canAnswer bool) map[string]any {
	out := map[string]any{
		"id": uuidToString(v.ID), "task_id": uuidToString(v.TaskID),
		"agent_id": uuidToString(v.AgentID), "mode": v.Mode,
		"questions": json.RawMessage(v.Questions), "status": v.Status,
		"reason": v.Reason.String, "version": v.Version,
		"expires_at": v.ExpiresAt, "detached_expires_at": v.DetachedExpiresAt,
		"can_answer":   canAnswer && (v.Status == "pending" || v.Status == "open"),
		"assign_count": v.AssignCount,
	}
	if v.ThreadID.Valid {
		out["comment_thread_id"] = uuidToString(v.ThreadID)
	}
	if len(v.Answer) != 0 {
		out["answer"] = json.RawMessage(v.Answer)
	}
	if v.AnsweredBy.Valid {
		out["answered_by"] = uuidToString(v.AnsweredBy)
	}
	if v.AnsweredAt.Valid {
		out["answered_at"] = v.AnsweredAt.Time
	}
	if v.ConsumedByTaskID.Valid {
		out["consumed_by_task_id"] = uuidToString(v.ConsumedByTaskID)
	}
	return out
}

func validateInteractionQuestions(questions []interactionQuestion) bool {
	if len(questions) == 0 || len(questions) > 5 {
		return false
	}
	seen := make(map[string]bool, len(questions))
	for _, q := range questions {
		if len(q.ID) == 0 || len(q.ID) > 64 || seen[q.ID] ||
			strings.TrimSpace(q.Question) == "" || len(q.Question) > 2000 || len(q.Options) > 10 {
			return false
		}
		seen[q.ID] = true
		for _, option := range q.Options {
			if strings.TrimSpace(option) == "" || len(option) > 500 {
				return false
			}
		}
	}
	return true
}

func validateInteractionAnswer(questionsJSON []byte, answer map[string]string) bool {
	var questions []interactionQuestion
	if json.Unmarshal(questionsJSON, &questions) != nil || len(answer) != len(questions) {
		return false
	}
	for _, q := range questions {
		value, ok := answer[q.ID]
		if !ok || strings.TrimSpace(value) == "" || len(value) > 4000 {
			return false
		}
		if len(q.Options) > 0 {
			found := false
			for _, option := range q.Options {
				if value == option {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

func decodeInteractionBody(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, interactionMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

// lockInteractionScope always precedes task and interaction row locks. The
// caller first reads immutable task identity without a row lock, then checks it
// again after taking this lock. No question is addressed by session ID alone.
func lockInteractionScope(ctx context.Context, tx pgx.Tx, workspaceID, issueID, agentID pgtype.UUID) error {
	key := uuidToString(workspaceID) + "/" + uuidToString(issueID) + "/" + uuidToString(agentID)
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "task-interaction/"+key)
	return err
}

func appendInteractionAudit(ctx context.Context, tx pgx.Tx, v interactionRecord, actorType string, actorID pgtype.UUID, action string) error {
	_, err := tx.Exec(ctx, `INSERT INTO task_interaction_audit
		(id, interaction_id, workspace_id, issue_id, task_id, actor_type, actor_id, action)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, dbid.NewV7(), v.ID, v.WorkspaceID, v.IssueID, v.TaskID, actorType, actorID, action)
	return err
}

func (h *Handler) interactionTaskForMember(w http.ResponseWriter, r *http.Request) (db.Issue, db.AgentTaskQueue, bool) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Issue{}, db.AgentTaskQueue{}, false
	}
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task id")
	if !ok {
		return db.Issue{}, db.AgentTaskQueue{}, false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || task.IssueID != issue.ID {
		writeError(w, http.StatusNotFound, "task not found")
		return db.Issue{}, db.AgentTaskQueue{}, false
	}
	return issue, task, true
}

func (h *Handler) canAnswerInteraction(ctx context.Context, issue db.Issue, task db.AgentTaskQueue, userID string) bool {
	if userID == "" {
		return false
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: task.AgentID, WorkspaceID: issue.WorkspaceID})
	return err == nil && h.canInvokeAgent(ctx, agent, "member", userID, userID, uuidToString(issue.WorkspaceID))
}

func (h *Handler) hasAssignableInteraction(ctx context.Context, task db.AgentTaskQueue) (bool, error) {
	if !task.IssueID.Valid {
		return false, nil
	}
	var pending bool
	err := h.DB.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM task_interaction i JOIN issue iss ON iss.id=i.issue_id
		JOIN agent_task_queue source ON source.id=i.task_id
		WHERE i.workspace_id=iss.workspace_id AND i.issue_id=$1 AND i.agent_id=$2
		AND i.comment_thread_id IS NOT DISTINCT FROM $3
		AND source.status IN ('completed','failed') AND i.status='answered_detached'
	)`, task.IssueID, task.AgentID, task.CommentThreadID).Scan(&pending)
	return pending, err
}

func (h *Handler) hydrateTaskInteractionMetadata(ctx context.Context, workspaceID pgtype.UUID, tasks []db.AgentTaskQueue, resp []AgentTaskResponse) {
	if len(tasks) == 0 || len(tasks) != len(resp) {
		return
	}
	ids := make([]pgtype.UUID, 0, len(tasks))
	for _, task := range tasks {
		if task.IssueID.Valid {
			ids = append(ids, task.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `SELECT task_id,
		bool_or(status='pending' AND expires_at>now()),
		bool_or(status='open' AND detached_expires_at>now())
		FROM task_interaction WHERE workspace_id=$1 AND task_id=ANY($2::uuid[])
		GROUP BY task_id`, workspaceID, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	type state struct{ waiting, needsInput bool }
	byTask := make(map[string]state)
	for rows.Next() {
		var id pgtype.UUID
		var v state
		if rows.Scan(&id, &v.waiting, &v.needsInput) != nil {
			return
		}
		byTask[uuidToString(id)] = v
	}
	for i, task := range tasks {
		v := byTask[uuidToString(task.ID)]
		if v.waiting && task.Status == "running" {
			resp[i].RunState = "waiting_on_user"
		}
		if v.needsInput {
			resp[i].InteractionOutcome = "needs_input"
		}
	}
}

// CreateDetachedInteraction is the nonblocking fallback for a process that has
// no negotiated live protocol. Only the token belonging to this exact task can
// call it. A caller cannot turn an arbitrary issue or another agent's run into
// an interactive prompt.
func (h *Handler) CreateDetachedInteraction(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Actor-Source") != "task_token" || r.Header.Get("X-Task-ID") != chi.URLParam(r, "taskId") {
		writeErrorCode(w, http.StatusForbidden, "task_token_required", "this action requires the current task token")
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task id")
	if !ok {
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || !task.IssueID.Valid || uuidToString(task.AgentID) != r.Header.Get("X-Agent-ID") {
		writeError(w, http.StatusNotFound, "issue task not found")
		return
	}
	var req struct {
		ClientRequestID string                `json:"client_request_id"`
		Questions       []interactionQuestion `json:"questions"`
	}
	if err := decodeInteractionBody(w, r, &req); err != nil || !validateInteractionQuestions(req.Questions) {
		writeError(w, http.StatusBadRequest, "invalid questions")
		return
	}
	requestID, ok := parseUUIDOrBadRequest(w, req.ClientRequestID, "client_request_id")
	if !ok {
		return
	}
	questions, _ := json.Marshal(req.Questions)
	var workspaceID pgtype.UUID
	if err := h.DB.QueryRow(r.Context(), `SELECT workspace_id FROM issue WHERE id=$1`, task.IssueID).Scan(&workspaceID); err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to create question")
		return
	}
	defer tx.Rollback(r.Context())
	if err := lockInteractionScope(r.Context(), tx, workspaceID, task.IssueID, task.AgentID); err != nil {
		writeError(w, 500, "failed to create question")
		return
	}
	existing, err := scanInteraction(tx.QueryRow(r.Context(), `SELECT `+interactionColumns+` FROM task_interaction
		WHERE task_id=$1 AND client_create_id=$2`, task.ID, requestID))
	if err == nil {
		if string(existing.Questions) != string(questions) {
			writeErrorCode(w, 409, "idempotency_conflict", "request id was used for different questions")
			return
		}
		writeJSON(w, http.StatusOK, interactionJSON(existing, false))
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "failed to create question")
		return
	}
	var currentStatus string
	if err := tx.QueryRow(r.Context(), `SELECT status FROM agent_task_queue WHERE id=$1 AND agent_id=$2 AND issue_id=$3 FOR UPDATE`, task.ID, task.AgentID, task.IssueID).Scan(&currentStatus); err != nil || currentStatus != "running" {
		writeErrorCode(w, http.StatusConflict, "run_ended", "the source run is no longer running")
		return
	}
	v, err := scanInteraction(tx.QueryRow(r.Context(), `INSERT INTO task_interaction
		(id, workspace_id, issue_id, agent_id, comment_thread_id, task_id, client_create_id,
		 mode, questions, status, reason, expires_at, detached_expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'detached',$8,'open','fallback',now()+interval '7 days',now()+interval '7 days')
		RETURNING `+interactionColumns,
		dbid.NewV7(), workspaceID, task.IssueID, task.AgentID, task.CommentThreadID, task.ID, requestID, questions))
	if err != nil {
		writeError(w, 500, "failed to create question")
		return
	}
	if err := appendInteractionAudit(r.Context(), tx, v, "agent", task.AgentID, "created"); err != nil {
		writeError(w, 500, "failed to create question")
		return
	}
	var inboxID pgtype.UUID
	err = tx.QueryRow(r.Context(), `INSERT INTO inbox_item
		(id, workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, body)
		SELECT $1,$2,'member',recipient.user_id,
		'task_interaction','action_required',$3,'Agent needs input',
		'Open this issue to answer the question. The agent will continue in a new run.'
		FROM agent_task_queue t JOIN agent a ON a.id=t.agent_id
		JOIN LATERAL (
			SELECT m.user_id FROM member m
			WHERE m.workspace_id=$2 AND m.user_id IN (t.accountable_user_id,a.owner_id)
			ORDER BY (m.user_id=t.accountable_user_id) DESC LIMIT 1
		) recipient ON true
		WHERE t.id=$4
		RETURNING id`, dbid.NewV7(), workspaceID, task.IssueID, task.ID).Scan(&inboxID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "failed to notify the responsible member")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to create question")
		return
	}
	h.publish("task:interaction_changed", uuidToString(workspaceID), "agent", uuidToString(task.AgentID), map[string]any{"issue_id": uuidToString(task.IssueID), "task_id": uuidToString(task.ID), "interaction_id": uuidToString(v.ID)})
	if inboxID.Valid {
		h.publish(protocol.EventInboxNew, uuidToString(workspaceID), "agent", uuidToString(task.AgentID), map[string]any{"inbox_item_id": uuidToString(inboxID), "issue_id": uuidToString(task.IssueID)})
	}
	writeJSON(w, http.StatusCreated, interactionJSON(v, false))
}

func (h *Handler) ListTaskInteractions(w http.ResponseWriter, r *http.Request) {
	issue, task, ok := h.interactionTaskForMember(w, r)
	if !ok {
		return
	}
	rows, err := h.DB.Query(r.Context(), `SELECT `+interactionColumns+` FROM task_interaction
		WHERE workspace_id=$1 AND issue_id=$2 AND task_id=$3 ORDER BY created_at, id`, issue.WorkspaceID, issue.ID, task.ID)
	if err != nil {
		writeError(w, 500, "failed to list questions")
		return
	}
	defer rows.Close()
	canAnswer := r.Header.Get("X-Actor-Source") == "" && h.canAnswerInteraction(r.Context(), issue, task, requestUserID(r))
	out := make([]map[string]any, 0)
	for rows.Next() {
		v, err := scanInteraction(rows)
		if err != nil {
			writeError(w, 500, "failed to list questions")
			return
		}
		out = append(out, interactionJSON(v, canAnswer))
	}
	if rows.Err() != nil {
		writeError(w, 500, "failed to list questions")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AnswerTaskInteraction(w http.ResponseWriter, r *http.Request) {
	h.mutateTaskInteraction(w, r, false)
}

func (h *Handler) CancelTaskInteraction(w http.ResponseWriter, r *http.Request) {
	h.mutateTaskInteraction(w, r, true)
}

func (h *Handler) mutateTaskInteraction(w http.ResponseWriter, r *http.Request, cancel bool) {
	issue, task, ok := h.interactionTaskForMember(w, r)
	if !ok {
		return
	}
	userID := requestUserID(r)
	if r.Header.Get("X-Actor-Source") != "" || !h.canAnswerInteraction(r.Context(), issue, task, userID) {
		writeErrorCode(w, http.StatusForbidden, "invocation_not_allowed", "you cannot answer for this agent")
		return
	}
	actorID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}
	interactionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "interactionId"), "interaction id")
	if !ok {
		return
	}
	var req struct {
		ExpectedVersion int               `json:"expected_version"`
		ClientRequestID string            `json:"client_request_id"`
		Answer          map[string]string `json:"answer"`
	}
	if err := decodeInteractionBody(w, r, &req); err != nil || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var requestID pgtype.UUID
	if !cancel {
		requestID, ok = parseUUIDOrBadRequest(w, req.ClientRequestID, "client_request_id")
		if !ok {
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "failed to update question")
		return
	}
	defer tx.Rollback(r.Context())
	if err := lockInteractionScope(r.Context(), tx, issue.WorkspaceID, issue.ID, task.AgentID); err != nil {
		writeError(w, 500, "failed to update question")
		return
	}
	var taskStatus string
	if err := tx.QueryRow(r.Context(), `SELECT status FROM agent_task_queue WHERE id=$1 AND issue_id=$2 AND agent_id=$3 FOR UPDATE`, task.ID, issue.ID, task.AgentID).Scan(&taskStatus); err != nil {
		writeError(w, 500, "failed to update question")
		return
	}
	var successorID pgtype.UUID
	var successorStatus string
	if cancel {
		err := tx.QueryRow(r.Context(), `SELECT id,status FROM agent_task_queue
			WHERE trigger_evidence_kind='interaction_answer' AND trigger_evidence_ref_id=$1
			AND issue_id=$2 AND agent_id=$3
			ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, interactionID, issue.ID, task.AgentID).
			Scan(&successorID, &successorStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 500, "failed to update question")
			return
		}
		if successorID.Valid && successorStatus != "queued" && successorStatus != "deferred" && successorStatus != "cancelled" {
			writeErrorCode(w, 409, "delivery_started", "the continuation has already started")
			return
		}
	}
	v, err := scanInteraction(tx.QueryRow(r.Context(), `SELECT `+interactionColumns+` FROM task_interaction
		WHERE id=$1 AND workspace_id=$2 AND issue_id=$3 AND task_id=$4 FOR UPDATE`, interactionID, issue.WorkspaceID, issue.ID, task.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "question not found")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to update question")
		return
	}
	if !cancel && v.AnsweredBy == actorID {
		var existingRequest pgtype.UUID
		if err := tx.QueryRow(r.Context(), `SELECT client_answer_id FROM task_interaction WHERE id=$1`, v.ID).Scan(&existingRequest); err == nil && existingRequest == requestID {
			body, _ := json.Marshal(req.Answer)
			if string(body) == string(v.Answer) {
				writeJSON(w, http.StatusOK, interactionJSON(v, false))
				return
			}
			writeErrorCode(w, 409, "idempotency_conflict", "request id was used for another answer")
			return
		}
	}
	if v.Version != req.ExpectedVersion || (v.Status != "pending" && v.Status != "open" && !(cancel && v.Status == "answered_detached")) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "interaction_conflict", "interaction": interactionJSON(v, false)})
		return
	}
	if !cancel && !validateInteractionAnswer(v.Questions, req.Answer) {
		writeError(w, 400, "invalid answer")
		return
	}
	if v.Status == "open" && time.Now().After(v.DetachedExpiresAt) {
		writeErrorCode(w, 409, "question_expired", "this question is no longer open")
		return
	}
	if cancel {
		v, err = scanInteraction(tx.QueryRow(r.Context(), `UPDATE task_interaction SET status='cancelled', reason='user_cancel', version=version+1, updated_at=now()
			WHERE id=$1 AND version=$2 RETURNING `+interactionColumns, v.ID, v.Version))
	} else {
		answer, _ := json.Marshal(req.Answer)
		status := "answered_detached"
		if v.Status == "pending" && taskStatus == "running" && time.Now().Before(v.ExpiresAt) {
			status = "answered"
		}
		v, err = scanInteraction(tx.QueryRow(r.Context(), `UPDATE task_interaction SET status=$2, answer=$3, answered_by=$4,
			answered_at=now(), client_answer_id=$5,
			reason=CASE WHEN status='pending' AND expires_at<=now() THEN 'expired' ELSE reason END,
			version=version+1, updated_at=now()
			WHERE id=$1 AND version=$6 RETURNING `+interactionColumns, v.ID, status, answer, actorID, requestID, v.Version))
	}
	if err != nil {
		writeError(w, 500, "failed to update question")
		return
	}
	action := "answered"
	if cancel {
		action = "cancelled"
	}
	if err := appendInteractionAudit(r.Context(), tx, v, "member", actorID, action); err != nil {
		writeError(w, 500, "failed to update question")
		return
	}
	if cancel && successorID.Valid && successorStatus != "cancelled" {
		var otherAnswers bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM task_interaction
			WHERE workspace_id=$1 AND issue_id=$2 AND agent_id=$3
			AND comment_thread_id IS NOT DISTINCT FROM $4 AND id<>$5
			AND status IN ('answered_detached','assigned'))`,
			issue.WorkspaceID, issue.ID, task.AgentID, task.CommentThreadID, v.ID).Scan(&otherAnswers); err != nil {
			writeError(w, 500, "failed to update question")
			return
		}
		if !otherAnswers {
			qtx := h.Queries.WithTx(tx)
			cancelled, err := qtx.CancelAgentTaskByUser(r.Context(), db.CancelAgentTaskByUserParams{
				ID: successorID, CancelledByType: pgtype.Text{String: "member", Valid: true}, CancelledByID: actorID,
			})
			if err != nil {
				writeError(w, 500, "failed to cancel continuation")
				return
			}
			if err := service.SettleTerminalTaskState(r.Context(), qtx, cancelled); err != nil {
				writeError(w, 500, "failed to cancel continuation")
				return
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "failed to update question")
		return
	}
	if v.Status == "answered_detached" {
		if _, err := h.TaskService.EnsureInteractionSuccessor(r.Context(), issue.WorkspaceID, issue.ID, task.AgentID); err != nil {
			slog.Warn("schedule clarification successor", "interaction_id", uuidToString(v.ID), "error", err)
		}
	}
	h.publish("task:interaction_changed", uuidToString(issue.WorkspaceID), "member", userID, map[string]any{"issue_id": uuidToString(issue.ID), "task_id": uuidToString(task.ID), "interaction_id": uuidToString(v.ID)})
	writeJSON(w, http.StatusOK, interactionJSON(v, false))
}
