import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";
import {
  ProviderTestOutcomeValues,
  type ProviderHealth,
  type ProvidersListResult,
} from "../../contracts/generated";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { translate } from "../../lib/i18n";
import { HealthBadge } from "./HealthBadge";
import { deadKeyNames, healthStatus, relativeTestTime } from "./health";
import { useProvidersStore } from "./useProvidersStore";

const health: ProviderHealth = {
  tested: true,
  outcome: "ok",
  dead: false,
  detail: "",
  tested_at: "2026-11-12T04:00:00Z",
};

describe("provider health", () => {
  const preferences = usePreferencesStore.getState();
  const providers = useProvidersStore.getState();
  afterEach(() => {
    usePreferencesStore.setState(preferences);
    useProvidersStore.setState(providers);
  });

  it("gives untested precedence, then the persisted dead flag, then outcome", () => {
    expect(healthStatus({ ...health, tested: false, dead: true })).toBe("untested");
    expect(healthStatus({ ...health, dead: true })).toBe("dead");
    expect(healthStatus(health)).toBe("ok");
    for (const outcome of ProviderTestOutcomeValues.filter((value) => value !== "ok")) {
      expect(healthStatus({ ...health, outcome })).toBe("unknown");
      expect(healthStatus({ ...health, outcome, dead: true })).toBe("dead");
    }
  });

  it("counts only dead active credentials and dead custom providers from the full snapshot", () => {
    const data: ProvidersListResult = {
      secret_backend: "keyring",
      endpoints: [],
      builtins: [
        {
          id: "builtin",
          display_name: "Builtin",
          docs_url: "",
          default_base_url: "",
          credential_groups: [],
          capabilities: ["llm"],
          configured: true,
          fields: [],
          credentials: [
            {
              id: "active",
              provider_id: "builtin",
              name: "Active",
              active: true,
              values: {},
              secret_hints: {},
              health: { ...health, dead: true },
              created_at: "",
              updated_at: "",
            },
            {
              id: "inactive",
              provider_id: "builtin",
              name: "Inactive",
              active: false,
              values: {},
              secret_hints: {},
              health: { ...health, dead: true },
              created_at: "",
              updated_at: "",
            },
          ],
        },
      ],
      custom: [
        {
          id: "custom",
          name: "Custom",
          discovery_format: "openai",
          base_url: "https://example.test",
          key_hint: "",
          health: { ...health, outcome: "secret_missing", dead: true },
          capabilities: [],
          models: [],
          created_at: "",
          updated_at: "",
        },
      ],
    };
    expect(deadKeyNames(data)).toEqual(["Builtin (Active)", "Custom"]);
    expect(deadKeyNames(null)).toEqual([]);
    data.builtins[0].credentials[0].health = health;
    data.custom[0].health = { ...health, outcome: "network" };
    expect(deadKeyNames(data)).toEqual([]);
  });

  it("formats test ages in the current language and ignores invalid timestamps", () => {
    const now = Date.parse("2026-11-12T04:02:00Z");
    expect(relativeTestTime(health.tested_at, "en", now)).toBe("2 minutes ago");
    expect(relativeTestTime(health.tested_at, "vi", now)).toBe(
      new Intl.RelativeTimeFormat("vi", { numeric: "auto" }).format(-2, "minute")
    );
    expect(relativeTestTime("", "en", now)).toBeNull();
    expect(relativeTestTime("invalid", "en", now)).toBeNull();
  });

  it("renders persisted health independently of transient manual test results", () => {
    usePreferencesStore.setState({ prefs: { ...preferences.prefs, language: "en" } });
    useProvidersStore.setState({
      testResults: {
        key: {
          ok: true,
          outcome: "ok",
          status: 200,
          latency_ms: 1,
          models: ["model-a"],
          detail: "",
        },
      },
    });
    const render = (value: ProviderHealth) =>
      renderToStaticMarkup(createElement(HealthBadge, { health: value }));
    const dead = render({
      ...health,
      dead: true,
      outcome: "secret_missing",
      detail: "Enter the key again",
    });
    expect(dead).toContain("Key dead");
    expect(dead).toContain("Key is missing; enter it again");
    expect(dead).toContain("Enter the key again");
    expect(render({ ...health, tested: false })).toContain("Not tested");
    expect(render(health)).toContain("Working");
    for (const outcome of [
      "timeout",
      "network",
      "rate_limited",
      "http_error",
      "not_found",
    ] as const) {
      expect(render({ ...health, outcome })).toContain("Could not verify");
    }
    expect(translate("vi", "providers.health.untested")).toBe("Chưa kiểm tra");
  });
});
