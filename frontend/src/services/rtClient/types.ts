import type {
  ConnectionStatus,
  ActionMessage,
  ResponseMessage,
  EventMessage,
} from "../../schemas/realtime.schema";

export type { ConnectionStatus, ActionMessage, ResponseMessage, EventMessage };

export type EventHandler = (data: unknown, refId?: string) => void;
export type StatusListener = (status: ConnectionStatus) => void;
