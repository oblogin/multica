package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func lockTaskInteractionScope(ctx context.Context, q *db.Queries, task db.AgentTaskQueue) error {
	if !task.IssueID.Valid {
		return nil
	}
	return q.LockIssueAgentInteractionScope(ctx, db.LockIssueAgentInteractionScopeParams{
		IssueID: task.IssueID, AgentID: task.AgentID,
	})
}

// EnsureInteractionSuccessor queues at most one compatible new run. It is
// called after an answer or source terminal transition. Assignment of answers
// happens later, in the recipient's claim finalization transaction.
func (s *TaskService) EnsureInteractionSuccessor(ctx context.Context, workspaceID, issueID, agentID pgtype.UUID) (*db.AgentTaskQueue, error) {
	if !workspaceID.Valid || !issueID.Valid || !agentID.Valid {
		return nil, nil
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin clarification successor: %w", err)
	}
	defer tx.Rollback(ctx)
	key := "task-interaction/" + util.UUIDToString(workspaceID) + "/" + util.UUIDToString(issueID) + "/" + util.UUIDToString(agentID)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return nil, err
	}
	qtx := s.Queries.WithTx(tx)
	var sourceID, interactionID, threadID pgtype.UUID
	var answeredBy pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT i.task_id, i.id, i.comment_thread_id, i.answered_by
		FROM task_interaction i JOIN agent_task_queue source ON source.id=i.task_id
		WHERE i.workspace_id=$1 AND i.issue_id=$2 AND i.agent_id=$3
		AND i.status='answered_detached' AND i.assign_count<2
		AND source.status IN ('completed','failed')
		ORDER BY i.answered_at, i.id LIMIT 1`, workspaceID, issueID, agentID).
		Scan(&sourceID, &interactionID, &threadID, &answeredBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Every in-flight state is considered, including deferred (which the
	// pending unique index does not cover). A running task can claim no new
	// prompt; its own terminal callback will make this check again.
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_task_queue
		WHERE issue_id=$1 AND agent_id=$2 AND comment_thread_id IS NOT DISTINCT FROM $3
		AND status IN ('queued','dispatched','deferred','running','waiting_local_directory'))`,
		issueID, agentID, threadID).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, nil
	}
	if err := guardIssueNotInTriage(ctx, qtx, issueID, OriginDerived); err != nil {
		if errors.Is(err, ErrIssueInTriage) {
			return nil, nil
		}
		return nil, err
	}
	currentIssue, err := qtx.GetIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	category := issuestatus.Category(ctx, qtx, workspaceID, currentIssue.Status)
	if category != issuestatus.CategoryStarted && category != issuestatus.CategoryUnstarted {
		return nil, nil
	}
	source, err := qtx.GetAgentTask(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	agent, err := qtx.GetAgent(ctx, agentID)
	if err != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return nil, err
	}
	var child db.AgentTaskQueue
	if source.Status == "failed" && source.FailureReason.Valid && retryableReasons[source.FailureReason.String] && retryEligible(source.FailureReason.String, source) {
		// Preserve the original comment batch and retry budget. No separate
		// continuation steals its queue slot.
		child, err = qtx.CreateRetryTask(ctx, db.CreateRetryTaskParams{
			NewTaskID: dbid.NewV7(), ID: source.ID,
			MaxAttempts: pgtype.Int4{Int32: retryAttemptCeiling(source.FailureReason.String, source.MaxAttempts), Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	} else {
		newID := dbid.NewV7()
		var insertedID pgtype.UUID
		err = tx.QueryRow(ctx, `INSERT INTO agent_task_queue (
			id, agent_id, runtime_id, issue_id, status, priority,
			comment_thread_id, trigger_comment_id, coalesced_comment_ids,
			trigger_summary, context, attempt, max_attempts, force_fresh_session,
			is_leader_task, squad_id, originator_user_id, accountable_user_id,
			originator_source,
			trigger_evidence_kind, trigger_evidence_ref_id)
			SELECT $1, source.agent_id, agent.runtime_id, source.issue_id, 'queued', source.priority,
				source.comment_thread_id, source.trigger_comment_id, source.coalesced_comment_ids,
				source.trigger_summary, source.context, 1, source.max_attempts, true,
				source.is_leader_task, source.squad_id, $2, $2,
				'direct_human',
				'interaction_answer', $3
			FROM agent_task_queue source JOIN agent ON agent.id=source.agent_id
			JOIN issue iss ON iss.id=source.issue_id
			WHERE source.id=$4 AND iss.workspace_id=$5 AND source.agent_id=$6
			AND agent.archived_at IS NULL AND agent.runtime_id IS NOT NULL
			ON CONFLICT DO NOTHING RETURNING id`, newID, answeredBy, interactionID, sourceID, workspaceID, agentID).Scan(&insertedID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		child, err = qtx.GetAgentTask(ctx, insertedID)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if child.Status == "queued" {
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, child)
		s.NotifyTaskEnqueued(ctx, child)
	}
	return &child, nil
}

// ExpireTaskInteractions owns the server-side deadlines. A response racing
// this CAS is resolved by the row lock: it becomes either live answered or a
// late detached answer, never an invented answer for the old process.
func (s *TaskService) ExpireTaskInteractions(ctx context.Context) (int64, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE task_interaction SET status='open', reason='expired', version=version+1, updated_at=now()
		WHERE status='pending' AND expires_at<=now()`)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE task_interaction SET status='abandoned', reason='detached_ttl', version=version+1, updated_at=now()
		WHERE status='open' AND detached_expires_at<=now()`); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// ReconcileInteractionSuccessors repairs the gap between the source task's
// terminal commit and scheduling. The scheduling function itself is locked and
// idempotent, so a crash or a concurrent answer cannot create two successors.
func (s *TaskService) ReconcileInteractionSuccessors(ctx context.Context) error {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT i.workspace_id, i.issue_id, i.agent_id
		FROM task_interaction i JOIN agent_task_queue source ON source.id=i.task_id
		WHERE i.status='answered_detached' AND i.assign_count<2
		AND source.status IN ('completed','failed') LIMIT 100`)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	type scope struct{ workspace, issue, agent pgtype.UUID }
	var scopes []scope
	for rows.Next() {
		var v scope
		if err := rows.Scan(&v.workspace, &v.issue, &v.agent); err != nil {
			rows.Close()
			tx.Rollback(ctx)
			return err
		}
		scopes = append(scopes, v)
	}
	err = rows.Err()
	rows.Close()
	tx.Rollback(ctx)
	if err != nil {
		return err
	}
	for _, v := range scopes {
		if _, err := s.EnsureInteractionSuccessor(ctx, v.workspace, v.issue, v.agent); err != nil {
			return err
		}
	}
	return nil
}
