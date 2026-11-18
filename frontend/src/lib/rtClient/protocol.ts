import { z } from "zod";
import type { ActionProgress } from "../../contracts/generated";

/** Runtime validation of server frames; static types come from contracts/generated.ts. */
const ErrorBodySchema = z.object({
  code: z.string().min(1),
  message: z.string(),
  details: z.record(z.string(), z.unknown()).optional(),
});

const ResponseFrameSchema = z.object({
  type: z.literal("response"),
  id: z.string(),
  success: z.boolean(),
  data: z.unknown().optional(),
  error: ErrorBodySchema.optional(),
});

const EventFrameSchema = z.object({
  type: z.literal("event"),
  event: z.string().min(1),
  ref_id: z.string().optional(),
  data: z.unknown(),
  timestamp: z.number(),
});

export const ServerFrameSchema = z.discriminatedUnion("type", [
  ResponseFrameSchema,
  EventFrameSchema,
]);

export type ServerFrame = z.infer<typeof ServerFrameSchema>;
export type ResponseFrame = z.infer<typeof ResponseFrameSchema>;
export type EventFrame = z.infer<typeof EventFrameSchema>;

export const ActionProgressSchema = z.object({
  percent: z.number(),
  message: z.string(),
  msg_key: z.string().optional(),
  msg_params: z.record(z.string(), z.string()).optional(),
  heartbeat: z.boolean().optional(),
});

export const RuntimeConfigSchema = z.object({
  ws_url: z.string().min(1),
  mode: z.string().optional(),
  version: z.string().optional(),
  action_timeout_ms: z.number().int().positive().optional(),
  long_action_timeout_ms: z.number().int().positive().optional(),
  ping_interval_ms: z.number().int().positive().optional(),
  check_updates_on_startup: z.boolean().optional(),
});

/** Validates an action:progress event payload; returns null when malformed. */
export function parseActionProgress(raw: unknown): ActionProgress | null {
  const parsed = ActionProgressSchema.safeParse(raw);
  return parsed.success ? parsed.data : null;
}

/** Parses one WebSocket text frame; returns null for anything malformed. */
export function parseServerFrame(raw: string): ServerFrame | null {
  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch {
    return null;
  }
  const parsed = ServerFrameSchema.safeParse(json);
  return parsed.success ? parsed.data : null;
}
