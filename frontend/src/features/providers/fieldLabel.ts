import { translateServerMessage, type Translate } from "../../lib/i18n";

export function fieldLabel(t: Translate, key: string): string {
  return translateServerMessage(t, `providers.field.${key}`, undefined) ?? key;
}
