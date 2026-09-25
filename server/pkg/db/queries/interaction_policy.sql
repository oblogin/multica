-- name: HasOpenSourceInteraction :one
SELECT EXISTS (
  SELECT 1 FROM task_interaction
  WHERE task_id=sqlc.arg('task_id')::uuid AND status='open'
) AS has_open;

-- name: LockIssueAgentInteractionScope :exec
-- Always call before taking task or interaction row locks.
SELECT pg_advisory_xact_lock(hashtextextended(
  'task-interaction/' || i.workspace_id::text || '/' || i.id::text || '/' || sqlc.arg('agent_id')::uuid::text, 0))
FROM issue i WHERE i.id=sqlc.arg('issue_id')::uuid;
