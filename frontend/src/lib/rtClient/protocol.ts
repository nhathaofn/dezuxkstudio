import { z } from "zod";

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

export const RuntimeConfigSchema = z.object({
  ws_url: z.string().min(1),
  mode: z.string().optional(),
  version: z.string().optional(),
});

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
