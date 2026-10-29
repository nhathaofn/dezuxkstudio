import React from "react";
import { Moon, Sun } from "lucide-react";
import { LanguageValues, type Language, type Theme } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { useT } from "../../lib/i18n";
import { resolveTheme } from "./theme";
import { usePreferencesStore } from "./usePreferencesStore";

/** One click switches between light and dark ("system" resolves to the OS scheme first). */
export const ThemeToggle: React.FC = () => {
  const t = useT();
  const theme = usePreferencesStore((s) => s.prefs.theme);
  const update = usePreferencesStore((s) => s.update);
  const current = resolveTheme(theme);
  const next: Theme = current === "light" ? "dark" : "light";

  return (
    <Button
      onClick={() => void update({ theme: next })}
      title={`${t("header.toggleTheme")}: ${t(`theme.${next}`)}`}
      aria-label={t("header.toggleTheme")}
    >
      {current === "light" ? <Moon size={14} /> : <Sun size={14} />}
    </Button>
  );
};

export const LanguageSelect: React.FC = () => {
  const t = useT();
  const language = usePreferencesStore((s) => s.prefs.language);
  const update = usePreferencesStore((s) => s.update);

  return (
    <select
      className="select-control"
      aria-label={t("header.language")}
      value={language}
      onChange={(e) => void update({ language: e.target.value as Language })}
    >
      {LanguageValues.map((l) => (
        <option key={l} value={l}>
          {t(`language.${l}`)}
        </option>
      ))}
    </select>
  );
};
