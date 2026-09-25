CREATE UNIQUE INDEX CONCURRENTLY task_interaction_live_uidx ON task_interaction (task_id, claim_generation, provider_request_id) WHERE mode = 'live';
