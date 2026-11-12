import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  Actions,
  Events,
  type ProvidersListResult,
  type ProviderTestResult,
  type ProviderDefaultsResult,
} from "../../contracts/generated";
import { ActionError, rtClient } from "../../lib/rtClient";
import { useConnectionStore } from "../connection/useConnectionStore";
import { useAgentStore } from "../agent/useAgentStore";
import { useBinariesStore } from "../binaries/useBinariesStore";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { useSystemStore } from "../system/useSystemStore";
import { bootstrap } from "../../app/bootstrap";
import { useProvidersStore } from "./useProvidersStore";
import { newModel } from "./customModels";

const data: ProvidersListResult = {
  secret_backend: "keyring",
  builtins: [],
  custom: [],
  endpoints: [],
  setting_specs: [],
};
const testResult: ProviderTestResult = {
  ok: true,
  outcome: "ok",
  status: 200,
  latency_ms: 42,
  detail: "",
  models: ["model-a"],
};

const initialState = useProvidersStore.getState();

describe("useProvidersStore", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    useProvidersStore.setState(initialState);
  });
  afterEach(() => vi.restoreAllMocks());

  it("loads the list through the generated action", async () => {
    const request = vi.spyOn(rtClient, "request").mockResolvedValueOnce(data);
    await useProvidersStore.getState().list();
    expect(request).toHaveBeenCalledWith(Actions.PROVIDERS_LIST, {});
    expect(useProvidersStore.getState()).toMatchObject({ data, loading: false, error: null });
  });

  it("keeps ActionError code and field details on list failure", async () => {
    const error = new ActionError("ERR_UNAVAILABLE", "", { field: "base_url" });
    vi.spyOn(rtClient, "request").mockRejectedValueOnce(error);
    await useProvidersStore.getState().list();
    expect(useProvidersStore.getState().error).toBe(error);
    expect(useProvidersStore.getState().loading).toBe(false);
  });

  it("creates credentials with non-secret values and secrets in the nested input", async () => {
    const request = vi
      .spyOn(rtClient, "request")
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce(data);
    const credential = {
      name: "Personal",
      values: { base_url: "https://api.example.test" },
      secrets: { api_key: "example-key" },
      clear_secrets: [],
    };
    await useProvidersStore.getState().createCredential("openai", credential);
    expect(request).toHaveBeenNthCalledWith(1, Actions.PROVIDERS_CREATE_CREDENTIAL, {
      provider_id: "openai",
      credential,
    });
    expect(request).toHaveBeenNthCalledWith(2, Actions.PROVIDERS_LIST, {});
    expect(useProvidersStore.getState().data).toEqual(data);
  });

  it("rethrows the original field error for form highlighting", async () => {
    const error = new ActionError("ERR_VALIDATION", "", { field: "api_key" });
    vi.spyOn(rtClient, "request").mockRejectedValueOnce(error);
    await expect(
      useProvidersStore
        .getState()
        .updateCredential("credential", { name: "", values: {}, secrets: {}, clear_secrets: [] })
    ).rejects.toBe(error);
    expect(useProvidersStore.getState().error?.details?.field).toBe("api_key");
  });

  it("wires changed events in bootstrap and reloads on every reconnect", () => {
    const on = vi.spyOn(rtClient, "on").mockImplementation(() => () => {});
    const onStatus = vi.spyOn(rtClient, "onStatus").mockImplementation(() => () => {});
    vi.spyOn(useConnectionStore.getState(), "connect").mockResolvedValue();
    vi.spyOn(usePreferencesStore.getState(), "load").mockResolvedValue();
    vi.spyOn(useSystemStore.getState(), "loadInfo").mockResolvedValue();
    vi.spyOn(useBinariesStore.getState(), "fetchBinaries").mockResolvedValue();
    for (const action of [
      "fetchProviders",
      "fetchHistory",
      "fetchApprovals",
      "fetchAllowedDirs",
    ] as const) {
      vi.spyOn(useAgentStore.getState(), action).mockResolvedValue();
    }
    const list = vi.spyOn(useProvidersStore.getState(), "list").mockResolvedValue();
    const defaults = vi.spyOn(useProvidersStore.getState(), "fetchDefaults").mockResolvedValue();
    bootstrap();
    const handler = on.mock.calls.find(([event]) => event === Events.PROVIDERS_CHANGED)?.[1];
    expect(handler).toBeDefined();
    useProvidersStore.setState({ data });
    const changed: ProvidersListResult = { ...data, secret_backend: "memory" };
    // The callback's generic event union is narrowed by the event assertion above.
    (handler as (payload: ProvidersListResult) => void)(changed);
    expect(useProvidersStore.getState().data).toBe(changed);
    expect(defaults).toHaveBeenCalledTimes(1);
    const statusHandler = onStatus.mock.calls[0][0];
    statusHandler("connected");
    statusHandler("reconnecting");
    statusHandler("connected");
    expect(list).toHaveBeenCalledTimes(2);
    expect(defaults).toHaveBeenCalledTimes(3);
  });

  it("does not overwrite a newer changed event with an old list response", async () => {
    vi.spyOn(useProvidersStore.getState(), "fetchDefaults").mockResolvedValue();
    let resolve!: (value: ProvidersListResult) => void;
    vi.spyOn(rtClient, "request").mockReturnValueOnce(
      new Promise<ProvidersListResult>((done) => {
        resolve = done;
      })
    );
    const pending = useProvidersStore.getState().list();
    const changed: ProvidersListResult = { ...data, secret_backend: "encrypted_file" };
    useProvidersStore.getState().receive(changed);
    resolve(data);
    await pending;
    expect(useProvidersStore.getState().data).toBe(changed);
    expect(useProvidersStore.getState().loading).toBe(false);
  });

  it("stores concurrent tests independently per credential or custom provider id", async () => {
    let resolve!: (value: ProviderTestResult) => void;
    const failure: ProviderTestResult = {
      ...testResult,
      ok: false,
      outcome: "auth_failed",
      status: 401,
    };
    const request = vi
      .spyOn(rtClient, "request")
      .mockReturnValueOnce(
        new Promise<ProviderTestResult>((done) => {
          resolve = done;
        })
      )
      .mockResolvedValueOnce(failure);
    const pending = useProvidersStore.getState().testCredential("credential-a");
    expect(useProvidersStore.getState().testing["credential-a"]).toBe(true);
    await useProvidersStore.getState().testCredential("credential-a");
    await useProvidersStore.getState().testCustom("custom-b");
    expect(useProvidersStore.getState().testing["credential-a"]).toBe(true);
    resolve(testResult);
    await pending;
    expect(request).toHaveBeenCalledTimes(2);
    expect(request).toHaveBeenNthCalledWith(1, Actions.PROVIDERS_TEST_CREDENTIAL, {
      id: "credential-a",
    });
    expect(request).toHaveBeenNthCalledWith(2, Actions.PROVIDERS_TEST_CUSTOM, { id: "custom-b" });
    expect(useProvidersStore.getState().testResults).toEqual({
      "credential-a": testResult,
      "custom-b": failure,
    });
    expect(useProvidersStore.getState().testing).toEqual({
      "credential-a": false,
      "custom-b": false,
    });
  });

  it("resets testing after a request error", async () => {
    const error = new ActionError("ERR_UNAVAILABLE", "");
    vi.spyOn(rtClient, "request").mockRejectedValueOnce(error);
    await expect(useProvidersStore.getState().testCustom("custom")).rejects.toBe(error);
    expect(useProvidersStore.getState().testing.custom).toBe(false);
  });

  it("passes custom_id and an empty key to discovery when editing a stored provider", async () => {
    const result = { models: [] };
    const request = vi.spyOn(rtClient, "request").mockResolvedValueOnce(result);
    const payload = {
      custom_id: "custom",
      discovery_format: "openai" as const,
      base_url: "https://example.test/v1",
      api_key: "",
    };
    await expect(useProvidersStore.getState().discoverModels(payload)).resolves.toBe(result);
    expect(request).toHaveBeenCalledWith(Actions.PROVIDERS_DISCOVER_MODELS, payload);
  });

  it("wraps each remaining mutation in its generated payload and refreshes", async () => {
    const request = vi.spyOn(rtClient, "request").mockResolvedValue(data);
    const store = useProvidersStore.getState();
    const credential = { name: "", values: {}, secrets: {}, clear_secrets: ["api_key"] };
    const provider = {
      name: "Example",
      discovery_format: "openai" as const,
      base_url: "https://example.test",
      api_key: "key",
      clear_api_key: false,
      models: [],
    };
    await store.updateCredential("a", credential);
    await store.deleteCredential("a");
    await store.activateCredential("b");
    await store.createCustom(provider);
    await store.updateCustom("c", provider);
    await store.deleteCustom("c");
    expect(request.mock.calls.filter(([action]) => action !== Actions.PROVIDERS_LIST)).toEqual([
      [Actions.PROVIDERS_UPDATE_CREDENTIAL, { id: "a", credential }],
      [Actions.PROVIDERS_DELETE_CREDENTIAL, { id: "a" }],
      [Actions.PROVIDERS_ACTIVATE_CREDENTIAL, { id: "b" }],
      [Actions.PROVIDERS_CREATE_CUSTOM, provider],
      [Actions.PROVIDERS_UPDATE_CUSTOM, { id: "c", provider }],
      [Actions.PROVIDERS_DELETE_CUSTOM, { id: "c" }],
    ]);
    expect(request.mock.calls.filter(([action]) => action === Actions.PROVIDERS_LIST)).toHaveLength(
      6
    );
  });

  it("loads defaults and stores the chosen-purpose response", async () => {
    const defaults: ProviderDefaultsResult = {
      purposes: [
        {
          purpose: "text",
          capability: "llm",
          chosen: "",
          effective: "",
          chosen_unavailable: false,
          candidates: [],
        },
      ],
    };
    const chosen: ProviderDefaultsResult = {
      purposes: [
        { ...defaults.purposes[0], chosen: "provider/model", effective: "provider/model" },
      ],
    };
    const request = vi
      .spyOn(rtClient, "request")
      .mockResolvedValueOnce(defaults)
      .mockResolvedValueOnce(chosen)
      .mockResolvedValueOnce(chosen);
    await useProvidersStore.getState().fetchDefaults();
    expect(useProvidersStore.getState().defaults).toEqual(defaults);
    await useProvidersStore.getState().setDefault("text", "provider/model");
    expect(request.mock.calls).toEqual([
      [Actions.PROVIDERS_DEFAULTS, {}],
      [Actions.PROVIDERS_SET_DEFAULT, { purpose: "text", model: "provider/model" }],
      [Actions.PROVIDERS_DEFAULTS, {}],
    ]);
    expect(useProvidersStore.getState().defaults).toEqual(chosen);
  });

  it("sets a purpose back to auto with an empty model ref", async () => {
    const request = vi.spyOn(rtClient, "request").mockResolvedValue({ purposes: [] });
    await useProvidersStore.getState().setDefault("image", "");
    expect(request).toHaveBeenCalledWith(Actions.PROVIDERS_SET_DEFAULT, {
      purpose: "image",
      model: "",
    });
  });

  it("replaces model lists, discovers builtin models, and patches settings through generated actions", async () => {
    const models = [newModel({ model_id: "a", capability: "llm", endpoint: "openai-chat" })];
    const request = vi.spyOn(rtClient, "request").mockResolvedValue(data);
    const list = vi.spyOn(useProvidersStore.getState(), "list").mockResolvedValue();
    const defaults = vi.spyOn(useProvidersStore.getState(), "fetchDefaults").mockResolvedValue();
    request.mockResolvedValueOnce({ models });
    await expect(useProvidersStore.getState().setModels("custom", models)).resolves.toEqual({
      models,
    });
    expect(request).toHaveBeenLastCalledWith(Actions.PROVIDERS_SET_MODELS, {
      owner_id: "custom",
      models,
    });
    request.mockResolvedValueOnce({ models });
    await expect(useProvidersStore.getState().discoverBuiltinModels("builtin")).resolves.toEqual({
      models,
    });
    expect(request).toHaveBeenLastCalledWith(Actions.PROVIDERS_DISCOVER_BUILTIN_MODELS, {
      id: "builtin",
    });
    request.mockResolvedValueOnce({ settings: { video_rpm: 20 } });
    await expect(
      useProvidersStore.getState().updateSettings("builtin", { video_rpm: "20", image_rpm: "" })
    ).resolves.toEqual({ settings: { video_rpm: 20 } });
    expect(request).toHaveBeenLastCalledWith(Actions.PROVIDERS_UPDATE_SETTINGS, {
      owner_id: "builtin",
      settings: { video_rpm: "20", image_rpm: "" },
    });
    expect(list).toHaveBeenCalledTimes(3);
    expect(defaults).toHaveBeenCalledTimes(2);
  });

  it("retains field, no_discovery and outcome errors for the new calls", async () => {
    const request = vi.spyOn(rtClient, "request");
    const store = useProvidersStore.getState();
    const cases = [
      {
        call: () => store.setModels("builtin", []),
        error: new ActionError("ERR_VALIDATION", "", { field: "models" }),
      },
      {
        call: () => store.setModels("builtin", []),
        error: new ActionError("ERR_VALIDATION", "", { field: "facts" }),
      },
      {
        call: () => store.updateSettings("builtin", { video_rpm: "-1" }),
        error: new ActionError("ERR_VALIDATION", "", { field: "video_rpm" }),
      },
      {
        call: () => store.setDefault("text", "missing/model"),
        error: new ActionError("ERR_VALIDATION", "", { field: "model" }),
      },
      {
        call: () => store.discoverBuiltinModels("builtin"),
        error: new ActionError("ERR_VALIDATION", "", { no_discovery: true }),
      },
      {
        call: () => store.discoverBuiltinModels("builtin"),
        error: new ActionError("ERR_UNAVAILABLE", "", { outcome: "timeout" }),
      },
    ];
    for (const { call, error } of cases) {
      request.mockRejectedValueOnce(error);
      await expect(call()).rejects.toBe(error);
      expect(useProvidersStore.getState().error).toBe(error);
    }
    const failure = new ActionError("ERR_UNAVAILABLE", "");
    request.mockRejectedValueOnce(failure);
    await store.fetchDefaults();
    expect(useProvidersStore.getState().error).toBe(failure);
  });

  it("ignores an older defaults snapshot after a changed event triggers a newer fetch", async () => {
    let resolve!: (value: ProviderDefaultsResult) => void;
    const newer: ProviderDefaultsResult = {
      purposes: [
        {
          purpose: "video",
          capability: "video",
          chosen: "",
          effective: "provider/new",
          chosen_unavailable: false,
          candidates: [],
        },
      ],
    };
    vi.spyOn(rtClient, "request")
      .mockReturnValueOnce(
        new Promise<ProviderDefaultsResult>((done) => {
          resolve = done;
        })
      )
      .mockResolvedValueOnce(newer);
    const pending = useProvidersStore.getState().fetchDefaults();
    useProvidersStore.getState().receive(data);
    await Promise.resolve();
    resolve({ purposes: [] });
    await pending;
    expect(useProvidersStore.getState().defaults).toEqual(newer);
  });
});
