import { z } from "zod";

const DateTimeSchema = z.string().refine((value) => Number.isFinite(Date.parse(value)));

export const TaskInteractionSchema = z.object({
  id: z.string().min(1),
  task_id: z.string().min(1),
  agent_id: z.string().min(1),
  mode: z.string().min(1),
  questions: z.array(z.object({
    id: z.string().min(1),
    question: z.string().min(1),
    options: z.array(z.string()).optional().default([]),
  })).min(1),
  status: z.string().min(1),
  reason: z.string().optional().default(""),
  version: z.number().int().positive(),
  expires_at: DateTimeSchema,
  detached_expires_at: DateTimeSchema,
  can_answer: z.boolean().optional().default(false),
  assign_count: z.number().int().nonnegative().optional().default(0),
  comment_thread_id: z.string().optional(),
  answer: z.record(z.string(), z.string()).optional(),
  answered_by: z.string().optional(),
  answered_at: DateTimeSchema.optional().catch(undefined),
  consumed_by_task_id: z.string().optional(),
}).loose();

export const TaskInteractionListSchema = z.array(z.unknown()).transform((items) =>
  items.flatMap((item) => {
    const parsed = TaskInteractionSchema.safeParse(item);
    return parsed.success ? [parsed.data] : [];
  }),
);
export type TaskInteraction = z.infer<typeof TaskInteractionSchema>;

export function canAnswerInteraction(interaction: TaskInteraction): boolean {
  return interaction.can_answer &&
    (interaction.status === "pending" || interaction.status === "open");
}
