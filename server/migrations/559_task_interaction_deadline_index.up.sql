CREATE INDEX CONCURRENTLY task_interaction_pending_deadline_idx ON task_interaction (expires_at) WHERE status = 'pending';
