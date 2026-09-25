// @vitest-environment node
import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import { TaskInteractionListSchema, canAnswerInteraction } from "./interaction-schema";

const valid = {
  id: "interaction", task_id: "source", agent_id: "agent", mode: "live",
  questions: [{ id: "scope", question: "Which scope?", options: ["A", "B"] }],
  status: "pending", reason: "", version: 1, expires_at: "2026-09-25T12:30:00Z",
  detached_expires_at: "2026-10-02T12:00:00Z", can_answer: true, assign_count: 0,
};

describe("task interaction response", () => {
  it("drops malformed records without hiding a valid question", () => {
    const rows = parseWithFallback([{
      status: "future_state", can_answer: true,
    }, valid], TaskInteractionListSchema, [], { endpoint: "GET interactions" });
    expect(rows).toHaveLength(1);
    expect(canAnswerInteraction(rows[0]!)).toBe(true);
  });

  it("keeps unknown states readable but never actionable", () => {
    const rows = TaskInteractionListSchema.parse([{ ...valid, status: "future_state" }]);
    expect(rows[0]?.status).toBe("future_state");
    expect(canAnswerInteraction(rows[0]!)).toBe(false);
  });

  it("does not turn a missing permission into an action", () => {
    const rows = TaskInteractionListSchema.parse([{ ...valid, can_answer: undefined }]);
    expect(canAnswerInteraction(rows[0]!)).toBe(false);
  });
});
