import { ResponseMessageSchema } from "../../schemas/realtime.schema";

interface PendingAction {
  resolve: (val: unknown) => void;
  reject: (err: Error) => void;
}

export class ActionDispatcher {
  private pending = new Map<string, PendingAction>();

  public createRequest(action: string, payload?: unknown): { id: string; raw: string } {
    const id = "act_" + Math.random().toString(36).substring(2, 9) + "_" + Date.now().toString(36);
    const raw = JSON.stringify({ id, action, payload });
    return { id, raw };
  }

  public register(
    id: string,
    resolve: (val: unknown) => void,
    reject: (err: Error) => void,
    timeoutMs = 60000
  ): void {
    this.pending.set(id, { resolve, reject });
    setTimeout(() => {
      if (this.pending.has(id)) {
        this.pending.delete(id);
        reject(new Error("Action execution timed out"));
      }
    }, timeoutMs);
  }

  public rejectAll(err: Error): void {
    for (const item of this.pending.values()) {
      item.reject(err);
    }
    this.pending.clear();
  }

  public handleResponse(raw: unknown): boolean {
    const parsed = ResponseMessageSchema.safeParse(raw);
    if (!parsed.success) return false;

    const res = parsed.data;
    if (res.id && this.pending.has(res.id)) {
      const { resolve, reject } = this.pending.get(res.id)!;
      this.pending.delete(res.id);
      if (res.success) {
        resolve(res.data);
      } else {
        reject(new Error(res.error || "Action failed"));
      }
      return true;
    }
    return false;
  }
}
