# Issue task interactions (server contract in progress)

This contract currently covers detached questions. Live Claude delivery is disabled until the `agentintegration` smoke test confirms the `AskUserQuestion` control protocol and resume behavior. Codex's empty elicitation response does not qualify as a capability. Chat tasks are outside this version.

## Endpoints

| Method and path | Actor | Effect |
| --- | --- | --- |
| `POST /api/tasks/{taskId}/interactions` | `mat_` token of that exact issue task | Save a detached question while the source task is running. Returns immediately. |
| `GET /api/issues/{issueId}/tasks/{taskId}/interactions` | Workspace member who can read the issue | List questions for one source task. |
| `POST /api/issues/{issueId}/tasks/{taskId}/interactions/{interactionId}/answer` | Human workspace member with `canInvokeAgent` | Accept one answer with version CAS and idempotency key. |
| `POST /api/issues/{issueId}/tasks/{taskId}/interactions/{interactionId}/cancel` | Same human permission | Close an open question. |

The client can poll the GET endpoint. `task:interaction_changed` WS events contain only `issue_id`, `task_id`, and `interaction_id`; fetch the question through the authorized GET. Creation also adds an action-required inbox item for the current accountable member (or agent owner when the accountable member has left). Its issue link opens the issue, where the task and interaction IDs identify the question.

## Payloads

Creation accepts at most 32 KiB of strict JSON. Each request has a UUID `client_request_id` and 1–5 questions. Each question has a unique `id`, nonempty `question` (at most 2,000 bytes), and optional `options` (at most 10, each at most 500 bytes). Replaying the same request ID and payload returns the existing interaction; changing the payload returns `409 idempotency_conflict`.

```json
{"client_request_id":"11111111-1111-4111-8111-111111111111","questions":[{"id":"scope","question":"Which scope?","options":["A","B"]}]}
```

An answer supplies the version most recently read, a UUID request ID, and one nonempty string per question ID (at most 4,000 bytes each). For a choice question the value must equal an offered option.

```json
{"expected_version":1,"client_request_id":"22222222-2222-4222-8222-222222222222","answer":{"scope":"A"}}
```

Cancellation supplies `expected_version`. A stale version or closed status returns `409` with `{"code":"interaction_conflict","interaction":<current DTO>}`. An accepted answer returns the interaction DTO. Replaying its request ID and identical body returns the saved DTO; reusing it for another answer returns `409 idempotency_conflict`. A member lacking invocation permission receives `403 invocation_not_allowed`; `mat_` cannot answer.

The DTO has `id`, `task_id`, `agent_id`, `mode`, `questions`, `status`, `reason`, `version`, `expires_at`, `detached_expires_at`, `can_answer`, and `assign_count`. It adds `comment_thread_id`, `answer`, `answered_by`, `answered_at`, and `consumed_by_task_id` when present. `can_answer` is advisory; the server checks permission again on mutation. `GET /task-runs` and agent task lists add `run_state=waiting_on_user` for a pending live question and `interaction_outcome=needs_input` for an open detached question; existing queue `status` values remain unchanged.

## Delivery boundary

The source task never receives a detached answer. Once it is terminal, a successor of the same workspace, issue, agent, and comment thread is queued or reused. Answers are assigned atomically at claim, only when the claiming daemon advertises `task-interaction-context-v1` and its task, runtime, and claim generation still match. Its prompt labels the answer as quoted context for a **new run** and includes interaction ID, source task, author, and time. An old daemon receives no assigned answer. `assigned` records prompt assignment, not model processing. At most two automatic assignments occur; manual work may still use a saved answer.

Detached questions remain answerable for seven days. Live questions, once supported, have a server-owned 30-minute default deadline and lead to `needs_input` rather than a fabricated answer. The server sweep is the deadline authority.

## Current rollout limit

This file describes the committed detached API. Live create/claim/ack, daemon waiter, watchdog pause, and provider smoke coverage are still required before any live capability can be offered. Do not present this endpoint as same-process delivery.
