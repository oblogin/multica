ALTER TABLE task_interaction_capability
    ADD COLUMN IF NOT EXISTS live_enabled BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE task_interaction
    ADD COLUMN IF NOT EXISTS process_nonce UUID;
