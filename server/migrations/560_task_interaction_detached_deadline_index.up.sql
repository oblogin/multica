CREATE INDEX CONCURRENTLY task_interaction_detached_deadline_idx ON task_interaction (detached_expires_at) WHERE status = 'open';
