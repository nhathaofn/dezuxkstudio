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

/** Human-readable message for an error: translated category plus server detail. */
export function describeError(t: Translate, err: ActionError): string {
  const key = `errors.${err.code}`;
  const category = isMessageKey(key) ? t(key) : t("errors.ERR_INTERNAL");
  return err.message && err.message !== category ? `${category} (${err.message})` : category;
}
