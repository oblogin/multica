"use client";

import { useRef, useState } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import { TaskInteractionSchema, canAnswerInteraction, type TaskInteraction } from "@multica/core/api/interaction-schema";
import { issueTasksOptions } from "@multica/core/issues/queries";
import { interactionKeys, taskInteractionsOptions } from "@multica/core/interactions/queries";
import { createInteractionRequestId } from "@multica/core/interactions/request-id";
import { useWorkspaceId } from "@multica/core/hooks";
import { useActorName } from "@multica/core/workspace/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

type Draft = Record<string, string>;

export function IssueInteractionsSection({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const taskQuery = useQuery(issueTasksOptions(issueId));
  const tasks = taskQuery.data ?? [];
  const queries = useQueries({
    queries: tasks.map((task) => taskInteractionsOptions(wsId ?? "", issueId, task.id)),
  });
  const interactions = queries.flatMap((query) => query.data ?? []);
  const hasError = taskQuery.isError || queries.some((query) => query.isError);
  const loading = taskQuery.isPending || queries.some((query) => query.isPending);
  const { t } = useT("issues");

  if (!interactions.length && !hasError && !loading) return null;
  return (
    <section aria-label={t(($) => $.interactions.title)} className="space-y-3">
      <h2 className="px-2 text-caption font-medium">{t(($) => $.interactions.title)}</h2>
      {loading && <p role="status" className="px-2 text-caption text-muted-foreground">{t(($) => $.interactions.loading)}</p>}
      {hasError && <div className="px-2 text-caption text-destructive">
        <p role="alert">{t(($) => $.interactions.load_error)}</p>
        <Button variant="outline" size="sm" onClick={() => { void taskQuery.refetch(); queries.forEach((query) => query.refetch()); }}>{t(($) => $.interactions.retry)}</Button>
      </div>}
      {interactions.map((interaction) => (
        <InteractionCard key={interaction.id} issueId={issueId} wsId={wsId ?? ""} interaction={interaction} />
      ))}
    </section>
  );
}

function InteractionCard({ issueId, wsId, interaction }: {
  issueId: string; wsId: string; interaction: TaskInteraction;
}) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const { getMemberName } = useActorName();
  const [draft, setDraft] = useState<Draft>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const request = useRef<{ body: string; id: string } | null>(null);
  const key = interactionKeys.task(wsId, issueId, interaction.task_id);
  const statusKey = interaction.status;
  const knownStatuses: Record<string, string> = {
    pending: t(($) => $.interactions.pending), answered: t(($) => $.interactions.answered),
    delivering: t(($) => $.interactions.delivering), delivered: t(($) => $.interactions.delivered),
    open: t(($) => $.interactions.open), answered_detached: t(($) => $.interactions.answered_detached),
    assigned: t(($) => $.interactions.assigned), settled: t(($) => $.interactions.settled),
    cancelled: t(($) => $.interactions.cancelled), void: t(($) => $.interactions.void),
    abandoned: t(($) => $.interactions.abandoned),
  };
  const knownReasons: Record<string, string> = {
    expired: t(($) => $.interactions.expired), process_lost: t(($) => $.interactions.process_lost),
    run_ended: t(($) => $.interactions.run_ended), possibly_delivered: t(($) => $.interactions.possibly_delivered),
    delivered_to_source: t(($) => $.interactions.delivered_to_source), user_cancel: t(($) => $.interactions.user_cancel),
    run_cancelled: t(($) => $.interactions.run_cancelled), detached_ttl: t(($) => $.interactions.detached_ttl),
  };
  const actionable = canAnswerInteraction(interaction);
  const cancellable = actionable || interaction.status === "answered_detached";
  const answer = Object.fromEntries(interaction.questions.map((q) => [q.id, (draft[q.id] ?? "").trim()]));
  const complete = interaction.questions.every((q) => {
    const value = answer[q.id];
    return !!value && (!q.options.length || q.options.includes(value));
  });

  function replaceInteraction(next: TaskInteraction) {
    qc.setQueryData<TaskInteraction[]>(key, (old) => old?.map((item) => item.id === next.id ? next : item) ?? [next]);
  }

  function handleError(cause: unknown) {
    if (cause instanceof ApiError && cause.status === 409) {
      const body = cause.body as { interaction?: unknown } | undefined;
      const latest = TaskInteractionSchema.safeParse(body?.interaction);
      if (latest.success && latest.data.id === interaction.id && latest.data.task_id === interaction.task_id) {
        replaceInteraction(latest.data);
      }
      request.current = null;
      setError(t(($) => $.interactions.conflict));
      void qc.invalidateQueries({ queryKey: key });
    } else if (cause instanceof ApiError && cause.status === 403) {
      setError(t(($) => $.interactions.forbidden));
      void qc.invalidateQueries({ queryKey: key });
    } else {
      setError(t(($) => $.interactions.network_error));
    }
  }

  async function sendAnswer() {
    if (!actionable || !complete || busy) return;
    setBusy(true);
    setError("");
    const body = JSON.stringify(answer);
    if (request.current?.body !== body) request.current = { body, id: createInteractionRequestId() };
    try {
      const next = await api.answerTaskInteraction(issueId, interaction.task_id, interaction.id,
        interaction.version, request.current.id, answer);
      replaceInteraction(next);
      request.current = null;
    } catch (cause) { handleError(cause); }
    finally { setBusy(false); }
  }

  async function cancel() {
    if (!cancellable || busy) return;
    setBusy(true);
    setError("");
    try {
      replaceInteraction(await api.cancelTaskInteraction(issueId, interaction.task_id, interaction.id, interaction.version));
    } catch (cause) { handleError(cause); }
    finally { setBusy(false); }
  }

  const deadline = interaction.status === "pending" ? interaction.expires_at : interaction.detached_expires_at;
  return <article className="rounded-md border border-border p-3 text-caption break-words">
    <p className="font-medium">{knownStatuses[statusKey] ?? t(($) => $.interactions.unknown_status, { status: interaction.status })}</p>
    {interaction.status === "pending" && interaction.mode === "live" &&
      <p className="text-muted-foreground">{t(($) => $.interactions.current_run)}</p>}
    {["open", "answered_detached", "assigned"].includes(interaction.status) &&
      <p className="text-muted-foreground">{t(($) => $.interactions.new_run)}</p>}
    <p className="text-muted-foreground">{t(($) => $.interactions.source_run)} <code className="break-all">{interaction.task_id}</code></p>
    {interaction.consumed_by_task_id && <p className="text-muted-foreground">{t(($) => $.interactions.successor_run)} <code className="break-all">{interaction.consumed_by_task_id}</code></p>}
    {interaction.reason && <p className="text-muted-foreground">{knownReasons[interaction.reason] ?? interaction.reason}</p>}
    {(interaction.status === "pending" || interaction.status === "open") && <p className="text-muted-foreground">
      {t(($) => $.interactions.deadline)} <time dateTime={deadline}>{new Date(deadline).toLocaleString()}</time>
    </p>}
    {interaction.questions.map((question) => <div key={question.id} className="mt-3 space-y-1">
      <p className="font-medium whitespace-pre-wrap">{question.question}</p>
      {interaction.answer?.[question.id] != null ? <p className="whitespace-pre-wrap">{interaction.answer[question.id]}</p> : actionable ?
        question.options.length ? <fieldset disabled={busy} className="space-y-1">
          <legend className="sr-only">{question.question}</legend>
          {question.options.map((option) => <label key={option} className="flex items-start gap-2">
            <input type="radio" name={`${interaction.id}-${question.id}`} value={option}
              checked={draft[question.id] === option} onChange={() => setDraft((old) => ({ ...old, [question.id]: option }))} />
            <span>{option}</span>
          </label>)}
        </fieldset> : <textarea className="w-full rounded-md border border-input bg-background p-2" rows={3}
          aria-label={question.question} maxLength={4000} disabled={busy} value={draft[question.id] ?? ""}
          onChange={(event) => setDraft((old) => ({ ...old, [question.id]: event.target.value }))} /> : null}
    </div>)}
    {interaction.answered_at && <p className="mt-2 text-muted-foreground">
      {t(($) => $.interactions.answered_by)} {interaction.answered_by ? getMemberName(interaction.answered_by) : "—"} · <time dateTime={interaction.answered_at}>{new Date(interaction.answered_at).toLocaleString()}</time>
    </p>}
    {interaction.status === "answered_detached" && interaction.assign_count >= 2 && <p>{t(($) => $.interactions.auto_stopped)}</p>}
    {error && <p role="alert" className="mt-2 text-destructive">{error}</p>}
    {cancellable && <div className="mt-3 flex flex-wrap gap-2">
      {actionable && <Button size="sm" onClick={sendAnswer} disabled={!complete || busy} aria-busy={busy}>
        {interaction.status === "open" ? t(($) => $.interactions.send_late) : t(($) => $.interactions.send)}
      </Button>}
      <Button size="sm" variant="outline" onClick={cancel} disabled={busy}>{t(($) => $.interactions.cancel)}</Button>
    </div>}
  </article>;
}
