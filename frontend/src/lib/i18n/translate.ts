import type { Language } from "../../contracts/generated";
import { en, type MessageKey } from "./en";
import { vi } from "./vi";

const catalogs: Record<Language, Record<MessageKey, string>> = { en, vi };

export type Translate = (key: MessageKey, vars?: Record<string, string | number>) => string;

const hasOwn = (obj: object, prop: string): boolean =>
  Object.prototype.hasOwnProperty.call(obj, prop);

/** Translates key for lang (falling back to English), interpolating {name} placeholders. */
export function translate(
  lang: Language,
  key: MessageKey,
  vars?: Record<string, string | number>
): string {
  const cat = catalogs[lang];
  const template = cat && hasOwn(cat, key) ? cat[key] : hasOwn(en, key) ? en[key] : String(key);
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (m, name: string) =>
    hasOwn(vars, name) ? String(vars[name]) : m
  );
}

export function isMessageKey(key: string): key is MessageKey {
  return hasOwn(en, key);
}
