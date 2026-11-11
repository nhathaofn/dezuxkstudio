import { useCallback } from "react";
import { usePreferencesStore } from "../../features/preferences/usePreferencesStore";
import type { ActionError } from "../rtClient";
import { isMessageKey, translate, type Translate } from "./translate";

export { translate, type Translate } from "./translate";
export type { MessageKey } from "./en";

/** Hook returning a translate function bound to the user's language preference. */
export function useT(): Translate {
  const lang = usePreferencesStore((s) => s.prefs.language);
  return useCallback<Translate>((key, vars) => translate(lang, key, vars), [lang]);
}

/**
 * Translates a server message key with its parameters, or returns undefined when the key is unknown
 * (e.g. an older/newer server), so callers can fall back to the server's English text.
 */
export function translateServerMessage(
  t: Translate,
  key: unknown,
  params: unknown
): string | undefined {
  if (typeof key !== "string" || !isMessageKey(key)) return undefined;
  const vars: Record<string, string> = {};
  if (params && typeof params === "object") {
    for (const [k, v] of Object.entries(params)) vars[k] = String(v);
  }
  return t(key, vars);
}

/** Human-readable message for an error: translated category plus server detail. */
export function describeError(t: Translate, err: ActionError): string {
  const specific = translateServerMessage(t, err.details?.msg_key, err.details?.msg_params);
  if (specific) return specific;
  const key = `errors.${err.code}`;
  const category = isMessageKey(key) ? t(key) : t("errors.ERR_INTERNAL");
  return err.message && err.message !== category ? `${category} (${err.message})` : category;
}
