import { z } from "zod";

// Connection status validator
export const ConnectionStatusSchema = z.enum([
  "connecting",
  "connected",
  "disconnected",
  "reconnecting",
]);
export type ConnectionStatus = z.infer<typeof ConnectionStatusSchema>;

// Outgoing action payload
export const ActionMessageSchema = z.object({
  id: z.string().min(1),
  action: z.string().min(1),
  payload: z.unknown().optional(),
});
export type ActionMessage = z.infer<typeof ActionMessageSchema>;

// Incoming response payload
export const ResponseMessageSchema = z.object({
  id: z.string().min(1),
  success: z.boolean(),
  data: z.unknown().optional(),
  error: z.string().optional(),
});
export type ResponseMessage = z.infer<typeof ResponseMessageSchema>;

// Incoming asynchronous event
export const EventMessageSchema = z.object({
  event: z.string().min(1),
  ref_id: z.string().optional(),
  data: z.unknown(),
  timestamp: z.number(),
});
export type EventMessage = z.infer<typeof EventMessageSchema>;

// Dynamic runtime configuration
export const RuntimeConfigSchema = z.object({
  ws_url: z.string().min(1),
  mode: z.string().optional(),
});
export type RuntimeConfig = z.infer<typeof RuntimeConfigSchema>;
