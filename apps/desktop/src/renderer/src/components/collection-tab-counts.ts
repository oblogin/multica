import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useCurrentWorkspace } from "@multica/core/paths";
import { autopilotListOptions } from "@multica/core/autopilots/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import {
  agentListOptions,
  skillListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import { buildRuntimeMachines } from "@multica/views/runtimes";
import { useDesktopRuntimeContext } from "./use-desktop-runtime-context";

/** Observe the same list caches as the pages, without fetching for every tab. */
export function useCollectionTabCounts(): Record<string, number | undefined> {
  const wsId = useCurrentWorkspace()?.id ?? "";
  const currentUserId = useAuthStore((s) => s.user?.id);
  const { localDaemonId, localMachineName } = useDesktopRuntimeContext();
  const agents = useQuery({ ...agentListOptions(wsId), enabled: false });
  const projects = useQuery({ ...projectListOptions(wsId), enabled: false });
  const autopilots = useQuery({ ...autopilotListOptions(wsId), enabled: false });
  const squads = useQuery({ ...squadListOptions(wsId), enabled: false });
  const skills = useQuery({ ...skillListOptions(wsId), enabled: false });
  const runtimes = useQuery({ ...runtimeListOptions(wsId), enabled: false });

  const machineCount = useMemo(() => {
    if (!runtimes.data || runtimes.isError) return undefined;
    return buildRuntimeMachines(runtimes.data, {
      now: Date.now(),
      localDaemonId,
      localMachineName,
      currentUserId,
      ensureLocalMachine: true,
    }).length;
  }, [runtimes.data, runtimes.isError, localDaemonId, localMachineName, currentUserId]);

  return {
    agents: agents.data && !agents.isError
      ? agents.data.filter((agent) => !agent.archived_at).length
      : undefined,
    projects: projects.isError ? undefined : projects.data?.length,
    autopilots: autopilots.isError ? undefined : autopilots.data?.length,
    squads: squads.isError ? undefined : squads.data?.length,
    skills: skills.isError ? undefined : skills.data?.length,
    runtimes: machineCount,
  };
}
