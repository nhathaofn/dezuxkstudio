import { ErrorCodeValues, type ErrorCode, type ErrorBody } from "../../contracts/generated";

/** Client-only codes for failures that never reached the server. */
export const ClientErrorCodes = {
  NOT_CONNECTED: "ERR_NOT_CONNECTED",
  DISCONNECTED: "ERR_DISCONNECTED",
} as const;

/** ErrorBody as received on the wire, before the code is validated. */
export type WireErrorBody = Omit<ErrorBody, "code"> & { code: string };

export type AnyErrorCode = ErrorCode | (typeof ClientErrorCodes)[keyof typeof ClientErrorCodes];

/** Error carrying a stable code; UI branches and translates on `code`. */
export class ActionError extends Error {
  readonly code: AnyErrorCode;
  readonly details?: Record<string, unknown>;

  constructor(code: AnyErrorCode, message: string, details?: Record<string, unknown>) {
    super(message);
    this.name = "ActionError";
    this.code = code;
    this.details = details;
  }

  static fromBody(body: WireErrorBody): ActionError {
    return new ActionError(normalizeCode(body.code), body.message, body.details);
  }
}

function normalizeCode(code: string): ErrorCode {
  return (ErrorCodeValues as readonly string[]).includes(code)
    ? (code as ErrorCode)
    : "ERR_INTERNAL";
}

function isErrorBody(v: unknown): v is WireErrorBody {
  return (
    typeof v === "object" &&
    v !== null &&
    typeof (v as WireErrorBody).code === "string" &&
    typeof (v as WireErrorBody).message === "string"
  );
}

/** Normalises anything thrown (ActionError, bridge ErrorBody, Error, string). */
export function toActionError(err: unknown): ActionError {
  if (err instanceof ActionError) return err;
  if (isErrorBody(err)) return ActionError.fromBody(err);
  if (typeof err === "string") {
    try {
      const parsed: unknown = JSON.parse(err);
      if (isErrorBody(parsed)) return ActionError.fromBody(parsed);
    } catch {
      // not JSON
    }
    return new ActionError("ERR_INTERNAL", err);
  }
  if (err instanceof Error) return new ActionError("ERR_INTERNAL", err.message);
  return new ActionError("ERR_INTERNAL", String(err));
}
