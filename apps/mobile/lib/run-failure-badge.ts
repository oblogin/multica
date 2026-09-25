import { i18n } from "./i18n/singleton";

/**
 * Short badge copy for a run's `failure_reason`, shown inline on the agent-runs
 * row next to the status word and a timestamp.
 *
 * Deliberately terser than `lib/failure-reason-label.ts`, which backs a
 * full-width chat bubble; this one shares a single line.
 *
 * Keyed by the raw wire value, not a closed enum: `failure_reason` is an open
 * string that grows as classifier rules land, and an installed build will meet
 * reasons it predates. An unrecognised reason returns undefined so the row
 * falls back to a bare status word — a compact badge is the one place where
 * web's raw-wire-value fallback would overflow the row.
 *
 * Lives in lib/ rather than inside run-row.tsx so the lookup is covered by
 * mobile's node-only vitest lane. AgentTaskSchema once erased refined reasons
 * before the row could look them up; the schema and badge tests guard that path.
 */
export function runFailureBadgeLabel(
  reason: string | null | undefined,
): string | undefined {
  if (!reason) return undefined;
  const key = `chat:failure_badge.${reason.replaceAll(".", "_")}`;
  return i18n.exists(key) ? i18n.t(key) : undefined;
}
