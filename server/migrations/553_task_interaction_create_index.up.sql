CREATE UNIQUE INDEX CONCURRENTLY task_interaction_create_uidx ON task_interaction (task_id, client_create_id) WHERE client_create_id IS NOT NULL;
