-- Interaction state is separate from the task queue so older servers can
-- continue scanning agent_task_queue during a staged deployment or rollback.
-- Relationships are validated and cleaned up by the application, not FKs.
CREATE TABLE task_interaction_capability (
    task_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    claim_generation TIMESTAMPTZ NOT NULL,
    capability TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE task_interaction (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    comment_thread_id UUID,
    task_id UUID NOT NULL,
    runtime_id UUID,
    claim_generation TIMESTAMPTZ,
    provider TEXT,
    provider_session_id TEXT,
    provider_request_id TEXT,
    client_create_id UUID,
    mode TEXT NOT NULL CHECK (mode IN ('live', 'detached')),
    questions JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'answered', 'delivering', 'delivered', 'open', 'answered_detached', 'assigned', 'settled', 'cancelled', 'void', 'abandoned')),
    reason TEXT,
    answer JSONB,
    answered_by UUID,
    answered_at TIMESTAMPTZ,
    client_answer_id UUID,
    expires_at TIMESTAMPTZ NOT NULL,
    detached_expires_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ,
    consumed_by_task_id UUID,
    assign_count INT NOT NULL DEFAULT 0 CHECK (assign_count >= 0),
    version INT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((mode = 'live' AND runtime_id IS NOT NULL AND claim_generation IS NOT NULL AND provider_request_id IS NOT NULL)
        OR (mode = 'detached' AND client_create_id IS NOT NULL))
);

CREATE TABLE task_interaction_audit (
    id UUID NOT NULL,
    interaction_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    task_id UUID,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'daemon', 'system')),
    actor_id UUID,
    action TEXT NOT NULL,
    detail JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
