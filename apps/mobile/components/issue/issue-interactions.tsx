import { useRef, useState } from "react";
import { ActivityIndicator, Pressable, TextInput, View } from "react-native";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { TaskInteractionSchema, canAnswerInteraction, type TaskInteraction } from "@multica/core/api/interaction-schema";
import { createInteractionRequestId } from "@multica/core/interactions/request-id";
import { Text } from "@/components/ui/text";
import { Button } from "@/components/ui/button";
import { api, ApiError } from "@/data/api";
import { issueInteractionsOptions, issueKeys, issueTasksOptions } from "@/data/queries/issues";
import { useWorkspaceStore } from "@/data/workspace-store";
import { useActorLookup } from "@/data/use-actor-name";
import { useT } from "@/lib/i18n";

export function IssueInteractions({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const taskQuery = useQuery(issueTasksOptions(wsId, issueId));
  const tasks = taskQuery.data ?? [];
  const queries = useQueries({ queries: tasks.map((task) => issueInteractionsOptions(wsId, issueId, task.id)) });
  const { t } = useT("issues");
  const interactions = queries.flatMap((query) => query.data ?? []);
  const loading = taskQuery.isPending || queries.some((query) => query.isPending);
  const failed = taskQuery.isError || queries.some((query) => query.isError);
  if (!interactions.length && !loading && !failed) return null;
  return <View className="px-4 py-3 gap-3 border-t border-border">
    <Text className="text-sm font-medium">{t("interactions.title")}</Text>
    {loading && <ActivityIndicator accessibilityLabel={t("interactions.loading")} />}
    {failed && <View className="gap-2">
      <Text accessibilityRole="alert" className="text-destructive">{t("interactions.load_error")}</Text>
      <Button variant="outline" onPress={() => { void taskQuery.refetch(); queries.forEach((query) => query.refetch()); }}><Text>{t("interactions.retry")}</Text></Button>
    </View>}
    {interactions.map((interaction) => <InteractionCard key={interaction.id} wsId={wsId} issueId={issueId} interaction={interaction} />)}
  </View>;
}

function InteractionCard({ wsId, issueId, interaction }: {
  wsId: string | null; issueId: string; interaction: TaskInteraction;
}) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const { getName } = useActorLookup();
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const request = useRef<{ body: string; id: string } | null>(null);
  const key = issueKeys.interactions(wsId, issueId, interaction.task_id);
  const statusKeys: Record<string, string> = {
    pending: "pending", answered: "answered", delivering: "delivering", delivered: "delivered",
    open: "open", answered_detached: "answered_detached", assigned: "assigned", settled: "settled",
    cancelled: "cancelled", void: "void", abandoned: "abandoned",
  };
  const reasonKeys: Record<string, string> = {
    expired: "expired", process_lost: "process_lost", run_ended: "run_ended",
    possibly_delivered: "possibly_delivered", delivered_to_source: "delivered_to_source",
    user_cancel: "user_cancel", run_cancelled: "run_cancelled", detached_ttl: "detached_ttl",
  };
  const actionable = canAnswerInteraction(interaction);
  const cancellable = actionable || interaction.status === "answered_detached";
  const answer = Object.fromEntries(interaction.questions.map((q) => [q.id, (draft[q.id] ?? "").trim()]));
  const complete = interaction.questions.every((q) => !!answer[q.id] && (!q.options.length || q.options.includes(answer[q.id])));

  function replaceInteraction(next: TaskInteraction) {
    qc.setQueryData<TaskInteraction[]>(key, (old) => old?.map((item) => item.id === next.id ? next : item) ?? [next]);
  }

  function handleError(cause: unknown) {
    if (cause instanceof ApiError && cause.status === 409) {
      const latest = TaskInteractionSchema.safeParse((cause.body as { interaction?: unknown } | undefined)?.interaction);
      if (latest.success && latest.data.id === interaction.id && latest.data.task_id === interaction.task_id) replaceInteraction(latest.data);
      request.current = null;
      setError(t("interactions.conflict"));
      void qc.invalidateQueries({ queryKey: key });
    } else if (cause instanceof ApiError && cause.status === 403) {
      setError(t("interactions.forbidden"));
      void qc.invalidateQueries({ queryKey: key });
    } else setError(t("interactions.network_error"));
  }

  async function send() {
    if (!actionable || !complete || busy) return;
    setBusy(true); setError("");
    const body = JSON.stringify(answer);
    if (request.current?.body !== body) request.current = { body, id: createInteractionRequestId() };
    try {
      replaceInteraction(await api.answerTaskInteraction(issueId, interaction.task_id, interaction.id,
        interaction.version, request.current.id, answer));
      request.current = null;
    } catch (cause) { handleError(cause); }
    finally { setBusy(false); }
  }

  async function cancel() {
    if (!cancellable || busy) return;
    setBusy(true); setError("");
    try { replaceInteraction(await api.cancelTaskInteraction(issueId, interaction.task_id, interaction.id, interaction.version)); }
    catch (cause) { handleError(cause); }
    finally { setBusy(false); }
  }

  const deadline = interaction.status === "pending" ? interaction.expires_at : interaction.detached_expires_at;
  return <View className="rounded-lg border border-border p-3 gap-2">
    <Text className="font-medium">{statusKeys[interaction.status]
      ? t(`interactions.${statusKeys[interaction.status]}`) : t("interactions.unknown_status", { status: interaction.status })}</Text>
    {interaction.status === "pending" && interaction.mode === "live" &&
      <Text className="text-muted-foreground text-xs">{t("interactions.current_run")}</Text>}
    {["open", "answered_detached", "assigned"].includes(interaction.status) &&
      <Text className="text-muted-foreground text-xs">{t("interactions.new_run")}</Text>}
    <Text selectable className="text-muted-foreground text-xs">{t("interactions.source_run")} {interaction.task_id}</Text>
    {interaction.consumed_by_task_id && <Text selectable className="text-muted-foreground text-xs">{t("interactions.successor_run")} {interaction.consumed_by_task_id}</Text>}
    {!!interaction.reason && <Text className="text-muted-foreground text-xs">{reasonKeys[interaction.reason]
      ? t(`interactions.${reasonKeys[interaction.reason]}`) : interaction.reason}</Text>}
    {(interaction.status === "pending" || interaction.status === "open") && <Text className="text-muted-foreground text-xs">
      {t("interactions.deadline")} {new Date(deadline).toLocaleString()}
    </Text>}
    {interaction.questions.map((question) => <View key={question.id} className="gap-2 mt-1">
      <Text className="font-medium">{question.question}</Text>
      {interaction.answer?.[question.id] != null ? <Text>{interaction.answer[question.id]}</Text> : actionable ?
        question.options.length ? <View className="gap-1">{question.options.map((option) =>
          <Pressable key={option} accessibilityRole="radio" accessibilityState={{ selected: draft[question.id] === option, disabled: busy }}
            accessibilityLabel={`${question.question}: ${option}`} disabled={busy}
            onPress={() => setDraft((old) => ({ ...old, [question.id]: option }))} className="flex-row items-start gap-2 py-1">
            <Text>{draft[question.id] === option ? "◉" : "○"}</Text><Text className="flex-1">{option}</Text>
          </Pressable>)}</View> : <TextInput multiline className="min-h-20 rounded-md border border-border bg-background px-3 py-2 text-foreground"
          accessibilityLabel={question.question} maxLength={4000} editable={!busy} value={draft[question.id] ?? ""}
          onChangeText={(value) => setDraft((old) => ({ ...old, [question.id]: value }))} /> : null}
    </View>)}
    {interaction.answered_at && <Text className="text-muted-foreground text-xs">{t("interactions.answered_by")} {interaction.answered_by ? getName("member", interaction.answered_by) : "—"} · {new Date(interaction.answered_at).toLocaleString()}</Text>}
    {interaction.status === "answered_detached" && interaction.assign_count >= 2 && <Text>{t("interactions.auto_stopped")}</Text>}
    {!!error && <Text accessibilityRole="alert" className="text-destructive">{error}</Text>}
    {cancellable && <View className="flex-row flex-wrap gap-2">
      {actionable && <Button size="sm" disabled={!complete || busy} onPress={send}><Text>{interaction.status === "open" ? t("interactions.send_late") : t("interactions.send")}</Text></Button>}
      <Button size="sm" variant="outline" disabled={busy} onPress={cancel}><Text>{t("interactions.cancel")}</Text></Button>
    </View>}
  </View>;
}
