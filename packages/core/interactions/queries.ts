import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const interactionKeys = {
  issue: (wsId: string, issueId: string) => ["interactions", wsId, issueId] as const,
  task: (wsId: string, issueId: string, taskId: string) =>
    [...interactionKeys.issue(wsId, issueId), taskId] as const,
};

export function taskInteractionsOptions(wsId: string, issueId: string, taskId: string) {
  return queryOptions({
    queryKey: interactionKeys.task(wsId, issueId, taskId),
    queryFn: () => api.listTaskInteractions(issueId, taskId),
    enabled: !!wsId && !!issueId && !!taskId,
    refetchInterval: 15_000,
  });
}
