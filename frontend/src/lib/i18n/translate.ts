import type { Language } from "../../contracts/generated";
import { en, type MessageKey } from "./en";
import { vi } from "./vi";

const catalogs: Record<Language, Record<MessageKey, string>> = { en, vi };

export type Translate = (key: MessageKey, vars?: Record<string, string | number>) => string;

/** Translates key for lang (falling back to English), interpolating {name} placeholders. */
export function translate(
  lang: Language,
  key: MessageKey,
  vars?: Record<string, string | number>
): string {
  const template = catalogs[lang]?.[key] ?? en[key];
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (m, name: string) =>
    name in vars ? String(vars[name]) : m
  );
}

export function isMessageKey(key: string): key is MessageKey {
  return key in en;
}
