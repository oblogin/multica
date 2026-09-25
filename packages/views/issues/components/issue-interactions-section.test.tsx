// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderWithI18n } from "../../test/i18n";
import { issueKeys } from "@multica/core/issues/queries";
import { interactionKeys } from "@multica/core/interactions/queries";
import type { TaskInteraction } from "@multica/core/api/interaction-schema";

const mock = vi.hoisted(() => ({ list: vi.fn(), answer: vi.fn(), cancel: vi.fn() }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws" }));
vi.mock("@multica/core/workspace/hooks", () => ({ useActorName: () => ({ getMemberName: () => "Member" }) }));
vi.mock("@multica/core/api", () => ({
  api: { listTaskInteractions: mock.list, answerTaskInteraction: mock.answer, cancelTaskInteraction: mock.cancel },
  ApiError: class ApiError extends Error {
    constructor(message: string, public status: number, public statusText: string, public body?: unknown) { super(message); }
  },
}));

import { ApiError } from "@multica/core/api";
import { IssueInteractionsSection } from "./issue-interactions-section";

const base: TaskInteraction = {
  id: "interaction", task_id: "task", agent_id: "agent", mode: "live",
  questions: [{ id: "scope", question: "Which scope?", options: ["A", "B"] }, { id: "note", question: "Any notes?", options: [] }],
  status: "pending", reason: "", version: 1, expires_at: "2026-09-25T12:30:00Z",
  detached_expires_at: "2026-10-02T12:00:00Z", can_answer: true, assign_count: 0,
};

function renderCard(interaction: TaskInteraction) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(issueKeys.tasks("issue"), [{ id: "task", status: "running" }]);
  qc.setQueryData(interactionKeys.task("ws", "issue", "task"), [interaction]);
  renderWithI18n(<QueryClientProvider client={qc}><IssueInteractionsSection issueId="issue" /></QueryClientProvider>);
  return qc;
}

beforeEach(() => {
  mock.list.mockReset().mockResolvedValue([base]);
  mock.answer.mockReset();
  mock.cancel.mockReset();
});

describe("issue clarifications", () => {
  it("sends every field to the source interaction with its version", async () => {
    mock.answer.mockResolvedValue({ ...base, status: "answered", answer: { scope: "A", note: "Use docs" } });
    renderCard(base);
    fireEvent.click(screen.getByRole("radio", { name: "A" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Any notes?" }), { target: { value: "Use docs" } });
    fireEvent.click(screen.getByRole("button", { name: "Send answer" }));
    await waitFor(() => expect(mock.answer).toHaveBeenCalledWith("issue", "task", "interaction", 1,
      expect.stringMatching(/^[0-9a-f-]{36}$/), { scope: "A", note: "Use docs" }));
    expect(await screen.findByText("Answer saved")).toBeVisible();
  });

  it("keeps the draft on 409 and can send it as a late answer", async () => {
    mock.list.mockResolvedValue([{ ...base, status: "open", version: 2 }]);
    mock.answer.mockRejectedValueOnce(new ApiError("conflict", 409, "Conflict", { interaction: { ...base, status: "open", version: 2 } }));
    mock.answer.mockResolvedValueOnce({ ...base, status: "answered_detached", version: 3 });
    renderCard(base);
    fireEvent.click(screen.getByRole("radio", { name: "A" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Any notes?" }), { target: { value: "Use docs" } });
    fireEvent.click(screen.getByRole("button", { name: "Send answer" }));
    expect(await screen.findByText(/Question changed/)).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Any notes?" })).toHaveValue("Use docs");
    fireEvent.click(screen.getByRole("button", { name: "Send for new run" }));
    await waitFor(() => expect(mock.answer).toHaveBeenLastCalledWith("issue", "task", "interaction", 2,
      expect.any(String), { scope: "A", note: "Use docs" }));
  });

  it("waits for cancellation and honors permissions and unknown states", async () => {
    mock.cancel.mockResolvedValue({ ...base, status: "cancelled" });
    const qc = renderCard(base);
    fireEvent.click(screen.getByRole("button", { name: "Cancel question" }));
    await waitFor(() => expect(mock.cancel).toHaveBeenCalledWith("issue", "task", "interaction", 1));
    expect(await screen.findByText("Cancelled")).toBeVisible();
    qc.setQueryData(interactionKeys.task("ws", "issue", "task"), [{ ...base, status: "future_state", can_answer: true }]);
    expect(await screen.findByText("Unknown state: future_state")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Send answer" })).toBeNull();
  });

  it("retries an unchanged answer with the same request ID", async () => {
    mock.answer.mockRejectedValueOnce(new Error("offline"));
    mock.answer.mockResolvedValueOnce({ ...base, status: "answered" });
    renderCard(base);
    fireEvent.click(screen.getByRole("radio", { name: "A" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Any notes?" }), { target: { value: "Keep it short" } });
    fireEvent.click(screen.getByRole("button", { name: "Send answer" }));
    expect(await screen.findByText(/Could not save/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Send answer" }));
    await waitFor(() => expect(mock.answer).toHaveBeenCalledTimes(2));
    expect(mock.answer.mock.calls[0]?.[4]).toBe(mock.answer.mock.calls[1]?.[4]);
  });

  it("shows a lost source process and the distinct successor run", () => {
    renderCard({ ...base, mode: "detached", status: "assigned", reason: "process_lost",
      can_answer: false, answer: { scope: "A", note: "Keep it short" }, consumed_by_task_id: "new-task" });
    expect(screen.getByText("Original process was lost")).toBeVisible();
    expect(screen.getByText(/The original run cannot resume/)).toBeVisible();
    expect(screen.getByText("new-task")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Send answer" })).toBeNull();
  });

  it("does not offer actions when the server denies answering", () => {
    renderCard({ ...base, can_answer: false });
    expect(screen.getByText("Waiting for your answer")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Send answer" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Cancel question" })).toBeNull();
  });

  it("shows a server-side permission refusal", async () => {
    mock.answer.mockRejectedValueOnce(new ApiError("denied", 403, "Forbidden", { code: "invocation_not_allowed" }));
    mock.list.mockResolvedValue([{ ...base, can_answer: false }]);
    renderCard(base);
    fireEvent.click(screen.getByRole("radio", { name: "A" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Any notes?" }), { target: { value: "Saved draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Send answer" }));
    expect(await screen.findByText("You cannot answer for this agent.")).toBeVisible();
    await waitFor(() => expect(screen.queryByRole("button", { name: "Send answer" })).toBeNull());
  });
});
