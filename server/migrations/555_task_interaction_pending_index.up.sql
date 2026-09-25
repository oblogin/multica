CREATE UNIQUE INDEX CONCURRENTLY task_interaction_pending_uidx ON task_interaction (task_id) WHERE status IN ('pending', 'answered', 'delivering');
