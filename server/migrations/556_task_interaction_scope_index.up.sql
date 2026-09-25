CREATE INDEX CONCURRENTLY task_interaction_scope_idx ON task_interaction (workspace_id, issue_id, agent_id, comment_thread_id, created_at DESC);
