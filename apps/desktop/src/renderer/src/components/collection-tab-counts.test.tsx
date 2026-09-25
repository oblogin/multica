import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { useCollectionTabCounts } from "./collection-tab-counts";

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws1" }),
}));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "me" } }),
}));
vi.mock("./use-desktop-runtime-context", () => ({
  useDesktopRuntimeContext: () => ({
    localDaemonId: null,
    localMachineName: null,
  }),
}));
vi.mock("@multica/views/runtimes", () => ({
  buildRuntimeMachines: () => [],
}));

describe("useCollectionTabCounts", () => {
  it("tracks active agents in the list cache through create and archive", async () => {
    const client = new QueryClient();
    const wrapper = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useCollectionTabCounts(), { wrapper });
    expect(result.current.agents).toBeUndefined();

    act(() => {
      client.setQueryData(workspaceKeys.agents("ws1"), [
        { id: "a1", archived_at: null },
        { id: "a2", archived_at: "2026-01-01T00:00:00Z" },
      ]);
    });
    await waitFor(() => expect(result.current.agents).toBe(1));

    act(() => {
      client.setQueryData(workspaceKeys.agents("ws1"), [
        { id: "a1", archived_at: null },
        { id: "a2", archived_at: "2026-01-01T00:00:00Z" },
        { id: "a3", archived_at: null },
      ]);
    });
    await waitFor(() => expect(result.current.agents).toBe(2));

    act(() => {
      client.setQueryData(workspaceKeys.agents("ws1"), [
        { id: "a1", archived_at: "2026-01-02T00:00:00Z" },
        { id: "a2", archived_at: "2026-01-01T00:00:00Z" },
        { id: "a3", archived_at: null },
      ]);
    });
    await waitFor(() => expect(result.current.agents).toBe(1));
  });
});
