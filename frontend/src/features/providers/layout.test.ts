import { describe, expect, it } from "vitest";
import type {
  BuiltinProvider,
  CustomProvider,
  ProviderCredential,
  ProviderHealth,
  ProvidersListResult,
} from "../../contracts/generated";
import {
  activeHealth,
  initialProvider,
  parseProviderLocation,
  providerGroups,
  providerLocation,
  providerName,
  providerStatus,
  type ProviderEntry,
} from "./layout";

const health: ProviderHealth = {
  tested: true,
  dead: false,
  outcome: "ok",
  detail: "",
  tested_at: "",
};
const key = (patch: Partial<ProviderCredential> = {}): ProviderCredential => ({
  id: "key",
  name: "Key",
  provider_id: "builtin",
  active: true,
  health,
  values: {},
  secret_hints: {},
  created_at: "",
  updated_at: "",
  ...patch,
});
const builtin = (id: string, patch: Partial<BuiltinProvider> = {}): BuiltinProvider => ({
  id,
  display_name: id,
  configured: false,
  credentials: [],
  capabilities: ["llm"],
  docs_url: "",
  default_base_url: "",
  credential_groups: [],
  fields: [],
  models: [],
  settings: {},
  setting_defaults: {},
  discoverable: false,
  ...patch,
});
const custom = (id: string): CustomProvider => ({
  id,
  name: id,
  capabilities: ["image"],
  health,
  base_url: "https://example.test",
  discovery_format: "openai",
  key_hint: "",
  models: [],
  settings: {},
  setting_defaults: {},
  created_at: "",
  updated_at: "",
});
const data: ProvidersListResult = {
  builtins: [
    builtin("Zulu", { configured: true, credentials: [key()] }),
    builtin("Alpha"),
    builtin("Beta", { configured: true, capabilities: ["video"] }),
  ],
  custom: [custom("Custom Z"), custom("Custom A")],
  secret_backend: "keyring",
  endpoints: [],
  setting_specs: [],
};

describe("provider sidebar layout", () => {
  it("places only dead active keys in attention, including custom providers, without duplicates", () => {
    const broken = { ...health, dead: true };
    const snapshot = {
      ...data,
      builtins: [
        builtin("Dead", { configured: true, credentials: [key({ health: broken })] }),
        builtin("Backup dead", {
          configured: true,
          credentials: [key(), key({ id: "backup", active: false, health: broken })],
        }),
        builtin("Timeout", {
          configured: true,
          credentials: [key({ health: { ...health, outcome: "network" } })],
        }),
      ],
      custom: [{ ...custom("Custom dead"), health: broken }],
    };
    const groups = providerGroups(snapshot);
    expect(groups.attention.map(providerName)).toEqual(["Custom dead", "Dead"]);
    expect(groups.configured.map(providerName)).toEqual(["Backup dead", "Timeout"]);
    expect(groups.custom).toEqual([]);
    expect(Object.values(groups).flat()).toHaveLength(4);
    expect(providerGroups(snapshot, "dead", "image").attention.map(providerName)).toEqual([
      "Custom dead",
    ]);
    expect(
      initialProvider({ ...snapshot, builtins: snapshot.builtins.slice(0, 1), custom: [] }, "")
        ?.provider.id
    ).toBe("Dead");
  });
  it("groups built-ins by configuration and sorts each group without mutating the snapshot", () => {
    const groups = providerGroups(data);
    expect(groups.configured.map(providerName)).toEqual(["Beta", "Zulu"]);
    expect(groups.unconfigured.map(providerName)).toEqual(["Alpha"]);
    expect(groups.custom.map(providerName)).toEqual(["Custom A", "Custom Z"]);
    expect(data.builtins.map((provider) => provider.id)).toEqual(["Zulu", "Alpha", "Beta"]);
    expect(providerGroups(null)).toEqual({
      attention: [],
      configured: [],
      unconfigured: [],
      custom: [],
    });
  });
  it("combines case-insensitive trimmed name/id search with capability filtering", () => {
    expect(providerGroups(data, "  ZUL  ", "llm").configured.map(providerName)).toEqual(["Zulu"]);
    expect(
      Object.values(providerGroups(data, "z", "image"))
        .flat()
        .map(providerName)
    ).toEqual(["Custom Z"]);
    expect(Object.values(providerGroups(data, "Alpha", "video")).flat()).toEqual([]);
  });
  it("uses only active credential health and maps transient failures to unverified", () => {
    const entry = (patch: Partial<BuiltinProvider>): ProviderEntry => ({
      kind: "builtin",
      provider: builtin("test", patch),
    });
    expect(providerStatus(entry({}))).toBe("unconfigured");
    expect(providerStatus(entry({ configured: true }))).toBe("unverified");
    expect(
      providerStatus(
        entry({ credentials: [key({ active: false, health: { ...health, dead: true } })] })
      )
    ).toBe("unconfigured");
    expect(providerStatus(entry({ credentials: [key()] }))).toBe("ok");
    expect(
      providerStatus(entry({ credentials: [key({ health: { ...health, dead: true } })] }))
    ).toBe("dead");
    expect(
      providerStatus(entry({ credentials: [key({ health: { ...health, tested: false } })] }))
    ).toBe("unverified");
    expect(
      providerStatus(entry({ credentials: [key({ health: { ...health, outcome: "network" } })] }))
    ).toBe("unverified");
    expect(providerStatus({ kind: "custom", provider: custom("local") })).toBe("ok");
    expect(activeHealth(entry({}))).toBeUndefined();
  });
  it("honors deep links, falls back after deletion, and leaves an unconfigured account at getting started", () => {
    expect(initialProvider(data, "Alpha")?.provider.id).toBe("Alpha");
    expect(initialProvider(data, "deleted")?.provider.id).toBe("Beta");
    expect(initialProvider({ ...data, builtins: [] }, "")?.provider.id).toBe("Custom A");
    expect(
      initialProvider({ ...data, builtins: [builtin("Alpha")], custom: [] }, "")
    ).toBeUndefined();
    expect(initialProvider(null, "Zulu")).toBeUndefined();
  });
});

describe("provider URL location", () => {
  it("parses defaults and safely handles missing or unknown sub-tabs", () => {
    expect(
      parseProviderLocation(new URLSearchParams("tab=providers&sub=defaults&p=a%2Fb"))
    ).toEqual({ sub: "defaults", providerId: "a/b" });
    expect(parseProviderLocation(new URLSearchParams("sub=unknown"))).toEqual({
      sub: "providers",
      providerId: "",
    });
  });
  it("preserves settings and unrelated params, including the selected provider on sub-tab changes", () => {
    const original = new URLSearchParams("tab=providers&p=one&extra=keep");
    const defaults = providerLocation(original, { sub: "defaults" });
    expect(defaults.get("p")).toBe("one");
    const next = providerLocation(defaults, { providerId: "khóa / custom", sub: "providers" });
    expect(parseProviderLocation(new URLSearchParams(next.toString()))).toEqual({
      sub: "providers",
      providerId: "khóa / custom",
    });
    expect(next.get("tab")).toBe("providers");
    expect(next.get("extra")).toBe("keep");
    expect(original.get("sub")).toBeNull();
    expect(providerLocation(next, { providerId: "" }).has("p")).toBe(false);
  });
});
