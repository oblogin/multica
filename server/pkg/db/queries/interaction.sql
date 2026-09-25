-- name: SettleTerminalTaskInteractions :execrows
-- Called in the terminal task transaction, after the task status changes.
-- The task row is already locked by the status update. A later answer always
-- locks that same row before the interaction row and sees the settled state.
UPDATE task_interaction AS i
SET status = CASE
      WHEN t.status = 'cancelled' THEN 'cancelled'
      WHEN i.consumed_by_task_id = t.id AND t.status = 'completed' THEN 'settled'
      WHEN i.consumed_by_task_id = t.id AND t.status = 'failed' THEN 'answered_detached'
      WHEN i.task_id = t.id AND i.status = 'pending' THEN 'open'
      WHEN i.task_id = t.id AND i.status IN ('answered','delivering','delivered') THEN 'answered_detached'
      ELSE i.status
    END,
    reason = CASE
      WHEN t.status = 'cancelled' THEN 'run_cancelled'
      WHEN i.task_id = t.id AND i.status = 'pending' AND t.status = 'failed' THEN 'process_lost'
      WHEN i.task_id = t.id AND i.status = 'pending' THEN 'run_ended'
      WHEN i.task_id = t.id AND i.status IN ('delivering','delivered') THEN 'possibly_delivered'
      ELSE i.reason
    END,
    consumed_by_task_id = CASE
      WHEN i.consumed_by_task_id = t.id AND t.status = 'failed' THEN NULL
      ELSE i.consumed_by_task_id
    END,
    version = i.version + 1,
    updated_at = now()
FROM agent_task_queue AS t
WHERE t.id = ANY(@task_ids::uuid[])
  AND t.status IN ('completed','failed','cancelled')
  AND ((i.task_id = t.id AND i.status IN ('pending','answered','delivering','delivered'))
    OR (i.consumed_by_task_id = t.id AND i.status = 'assigned')
    OR (t.status = 'cancelled' AND i.task_id = t.id AND i.status IN ('open','answered_detached')));

-- name: ListWaitingInteractionTaskIDs :many
SELECT DISTINCT task_id FROM task_interaction
WHERE workspace_id = @workspace_id AND task_id = ANY(@task_ids::uuid[])
  AND status = 'pending' AND expires_at > now();

-- name: AssignTaskInteractionAnswers :many
-- The destination must be the exact dispatched task and claim generation
-- whose response is being finalized. The source must already be terminal.
-- The recipient's thread, workspace, issue and agent must all match.
UPDATE task_interaction AS i
SET status='assigned', consumed_by_task_id=target.id,
    assign_count=assign_count+1, version=version+1, updated_at=now()
FROM agent_task_queue AS target, agent_task_queue AS source, issue AS issue
WHERE target.id=sqlc.arg('claim_task_id')::uuid AND target.runtime_id=sqlc.arg('claim_runtime_id')::uuid
  AND target.dispatched_at=sqlc.arg('claim_dispatched_at')::timestamptz AND target.status='dispatched'
  AND target.issue_id IS NOT NULL AND issue.id=target.issue_id
  AND i.workspace_id=issue.workspace_id AND i.issue_id=target.issue_id
  AND i.agent_id=target.agent_id
  AND i.comment_thread_id IS NOT DISTINCT FROM target.comment_thread_id
  AND i.task_id=source.id AND source.status IN ('completed','failed')
  AND source.id<>target.id AND i.status='answered_detached'
  AND EXISTS (SELECT 1 FROM task_interaction_capability cap
    WHERE cap.task_id=target.id AND cap.runtime_id=target.runtime_id
      AND cap.claim_generation=target.dispatched_at
      AND cap.capability='task-interaction-context-v1')
  AND (i.assign_count < 2 OR target.trigger_evidence_kind IS DISTINCT FROM 'interaction_answer')
RETURNING i.id, i.task_id AS source_task_id, i.questions, i.answer,
          i.answered_by, i.answered_at, i.reason, i.assign_count;

-- name: RecordTaskInteractionContextCapability :exec
INSERT INTO task_interaction_capability
    (task_id, workspace_id, issue_id, runtime_id, claim_generation, capability)
SELECT t.id, iss.workspace_id, t.issue_id, t.runtime_id, t.dispatched_at,
       'task-interaction-context-v1'
FROM agent_task_queue t JOIN issue iss ON iss.id=t.issue_id
WHERE t.id=sqlc.arg('task_id')::uuid
  AND t.runtime_id=sqlc.arg('runtime_id')::uuid
  AND t.dispatched_at=sqlc.arg('dispatched_at')::timestamptz
  AND t.status='dispatched'
ON CONFLICT (task_id) DO UPDATE
SET runtime_id=EXCLUDED.runtime_id,
    claim_generation=EXCLUDED.claim_generation,
    capability=EXCLUDED.capability;

-- name: RecordTaskInteractionAssignment :exec
INSERT INTO task_interaction_audit
    (id, interaction_id, workspace_id, issue_id, task_id, actor_type, action)
SELECT gen_random_uuid(), i.id, i.workspace_id, i.issue_id, sqlc.arg('recipient_task_id')::uuid,
       'system', 'assigned'
FROM task_interaction i
WHERE i.id=sqlc.arg('interaction_id')::uuid
  AND i.consumed_by_task_id=sqlc.arg('recipient_task_id')::uuid;
