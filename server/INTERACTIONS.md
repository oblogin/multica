# Issue task interactions

Detached questions are available to issue tasks. Live Claude delivery is gated by `MULTICA_LIVE_INTERACTIONS=1` and remains disabled in normal builds until the gated `agentintegration` smoke test confirms the installed Claude CLI protocol. Codex's empty elicitation response does not qualify as a capability. Chat tasks are outside this version.

## Endpoints

| Method and path | Actor | Effect |
| --- | --- | --- |
| `POST /api/tasks/{taskId}/interactions` | `mat_` token of that exact issue task | Save a detached question while the source task is running. Returns immediately. |
| `GET /api/issues/{issueId}/tasks/{taskId}/interactions` | Workspace member who can read the issue | List questions for one source task. |
| `POST /api/issues/{issueId}/tasks/{taskId}/interactions/{interactionId}/answer` | Human workspace member with `canInvokeAgent` | Accept one answer with version CAS and idempotency key. |
| `POST /api/issues/{issueId}/tasks/{taskId}/interactions/{interactionId}/cancel` | Same human permission | Close an open question. |
| `POST /api/daemon/tasks/{taskId}/interactions` | Authenticated daemon for the task workspace | Create or replay a live question for the negotiated Claude run. |
| `POST /api/daemon/tasks/{taskId}/interactions/{interactionId}/claim` | Same daemon | Poll for the human answer and claim its delivery. `204` means pending. |
| `POST /api/daemon/tasks/{taskId}/interactions/{interactionId}/ack` | Same daemon | Record that the control response was written to Claude stdin. |

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

```json
{"id":"11111111-1111-4111-8111-111111111111","task_id":"22222222-2222-4222-8222-222222222222","agent_id":"33333333-3333-4333-8333-333333333333","mode":"live","questions":[{"id":"q0","question":"Which scope?","options":["A","B"]}],"status":"pending","reason":"","version":1,"expires_at":"2026-09-25T12:30:00Z","detached_expires_at":"2026-10-02T12:00:00Z","can_answer":true,"assign_count":0}
```

`pending` is a live question still waiting for a human; `answered` awaits the daemon; `delivering` means the daemon claimed the answer but the provider write is unconfirmed; `delivered` means bytes were written, not that Claude processed them. `open` is a question answerable after the live deadline or source run ended. `answered_detached` is saved for a new run; `assigned` is included in a new claim's prompt; `settled` is resolved by a completed source or successor. `cancelled`, `void`, and `abandoned` are closed. Recognized reasons are `expired`, `process_lost`, `run_ended`, `possibly_delivered`, `delivered_to_source`, `user_cancel`, `run_cancelled`, and `detached_ttl`; clients should show an unknown reason or status safely, without presenting an answer action unless `can_answer` and the known status allow it.

The UI should validate every response through a schema. For example, `{"status":"future_state","can_answer":true}` lacks identity and version, so it cannot become an actionable question; an otherwise complete DTO with an unknown status remains readable but is not answerable. The source task ID and `agent_id` identify the run in history; a later `consumed_by_task_id` is a different run and must be labelled as such.

## Delivery boundary

The source task never receives a detached answer. Once it is terminal, a successor of the same workspace, issue, agent, and comment thread is queued or reused. Answers are assigned atomically at claim, only when the claiming daemon advertises `task-interaction-context-v1` and its task, runtime, and claim generation still match. Its prompt labels the answer as quoted context for a **new run** and includes interaction ID, source task, author, and time. An old daemon receives no assigned answer. `assigned` records prompt assignment, not model processing. At most two automatic assignments occur; manual work may still use a saved answer.

Detached questions remain answerable for seven days. Live questions, once supported, have a server-owned 30-minute default deadline and lead to `needs_input` rather than a fabricated answer. The server sweep is the deadline authority.

## Live Claude protocol

The daemon advertises `task-interaction-live-v1` on an issue task with a modern claim. The server echoes `interaction_live_capability` in the start response only when `MULTICA_LIVE_INTERACTIONS=1`, the persisted task capability matches that runtime and claim generation, and the runtime provider is Claude. The daemon opens a live waiter only after this echo. Older servers and daemons therefore fail closed. The flag is off by default.

All three daemon requests carry `runtime_id`, `dispatched_at` (RFC3339 claim generation), `process_nonce` (a fresh UUID for each Claude process), and `provider_request_id`. The create request also carries the `questions` array described above. The provider request ID is stable across transport retries and scoped to the process nonce. The server rejects a mismatched task, runtime, generation, nonce, request ID, or changed question payload. A repeated create with the same values returns the same interaction. The daemon polls claim until it receives `{"interaction_id":"...","answer":{"q0":"A"}}`; it then writes exactly one `control_response` with human answers as Claude's `updatedInput.answers` and posts ack. No answer is synthesized on failure or timeout. If stdin delivery is uncertain, the record retains `delivering`; terminal failure marks the saved answer `possibly_delivered` for a later run. A completed source run settles a claimed or delivered answer without replaying it.

The daemon routes require a daemon token for that workspace; a task token cannot claim or acknowledge. Create requires a running issue task with a negotiated live capability. A wrong runtime or claim returns `409 stale_claim`; a different process nonce/request ID cannot read the question (`404`), and reusing the same request ID for changed input returns `409 idempotency_conflict`. Disabled live support returns `412 live_interaction_disabled`; a run without the negotiated capability returns `412 live_capability_required`. Human answers still use the member API and its `expected_version` and stable `client_request_id`. Poll the authorized task interaction GET after `task:interaction_changed` or periodically if WS is disconnected. Inbox notifications link to the issue; the event carries identifiers only, never question or answer text.

Only single-choice and free-text Claude questions are supported in the live path. A multi-select question receives an explicit refusal instructing the agent to create a detached question with `multica task ask`; that detached question is then visible in the issue and inbox. The daemon emits periodic waiting status while polling, and its normal cancellation stops polling and the Claude process.

The live path must remain disabled until a real Claude `agentintegration` smoke has verified the control request and response against the installed CLI. Real-agent smoke tests require explicit authorization under `AGENTS.md`.

On 2026-09-25, one authorized smoke run on Haiku and one on Sonnet both reported that `AskUserQuestion` was unavailable in the current non-interactive CLI session. Neither run produced a control request or an acknowledgement. This is a failed compatibility gate, so the live feature flag must stay off. Anthropic's documented `canUseTool` question flow is an Agent SDK integration; the current raw CLI invocation has not demonstrated equivalent behavior.
