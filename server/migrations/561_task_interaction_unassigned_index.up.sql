CREATE INDEX CONCURRENTLY task_interaction_unassigned_idx ON task_interaction (workspace_id, issue_id, agent_id, comment_thread_id) WHERE status = 'answered_detached';
