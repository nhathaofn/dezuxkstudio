import { create } from "zustand";
import {
  Actions,
  type CredentialInput,
  type CustomProviderInput,
  type DiscoverModelsPayload,
  type DiscoverModelsResult,
  type ProvidersListResult,
  type ProviderTestResult,
  type ProviderModel,
  type ProviderModelsResult,
  type ProviderSettingsResult,
  type ProviderDefaultsResult,
  type ProviderPurpose,
} from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

interface ProvidersState {
  data: ProvidersListResult | null;
  defaults: ProviderDefaultsResult | null;
  fetchDefaults: () => Promise<void>;
  setModels: (ownerId: string, models: ProviderModel[]) => Promise<ProviderModelsResult>;
  discoverBuiltinModels: (id: string) => Promise<ProviderModelsResult>;
  updateSettings: (
    ownerId: string,
    settings: Record<string, string>
  ) => Promise<ProviderSettingsResult>;
  setDefault: (purpose: ProviderPurpose, model: string) => Promise<void>;
  loading: boolean;
  error: ActionError | null;
  testing: Record<string, boolean>;
  testResults: Record<string, ProviderTestResult>;
  receive: (data: ProvidersListResult) => void;
  list: () => Promise<void>;
  createCredential: (providerId: string, credential: CredentialInput) => Promise<void>;
  updateCredential: (id: string, credential: CredentialInput) => Promise<void>;
  deleteCredential: (id: string) => Promise<void>;
  activateCredential: (id: string) => Promise<void>;
  testCredential: (id: string) => Promise<void>;
  createCustom: (provider: CustomProviderInput) => Promise<void>;
  updateCustom: (id: string, provider: CustomProviderInput) => Promise<void>;
  deleteCustom: (id: string) => Promise<void>;
  testCustom: (id: string) => Promise<void>;
  discoverModels: (payload: DiscoverModelsPayload) => Promise<DiscoverModelsResult>;
}

export const useProvidersStore = create<ProvidersState>((set, get) => {
  // An event received while a list is pending is newer than that list snapshot.
  let revision = 0;
  let listSequence = 0;
  let defaultsSequence = 0;
  const run = async <T>(request: () => Promise<T>): Promise<T> => {
    set({ error: null });
    try {
      return await request();
    } catch (err) {
      const error = toActionError(err);
      set({ error });
      throw error;
    }
  };
  const mutate = async (request: () => Promise<unknown>, invalidateId?: string) => {
    await run(request);
    if (invalidateId)
      set((s) => {
        const testResults = { ...s.testResults };
        delete testResults[invalidateId];
        return { testResults };
      });
    // Events update all clients; refresh also makes mutations usable before the event arrives.
    await get().list();
  };
  const test = async (
    id: string,
    action: typeof Actions.PROVIDERS_TEST_CREDENTIAL | typeof Actions.PROVIDERS_TEST_CUSTOM
  ) => {
    if (get().testing[id]) return;
    set((s) => {
      const testResults = { ...s.testResults };
      delete testResults[id];
      return { testing: { ...s.testing, [id]: true }, testResults };
    });
    try {
      const result = await run(() => rtClient.request(action, { id }));
      set((s) => ({ testResults: { ...s.testResults, [id]: result } }));
    } finally {
      set((s) => ({ testing: { ...s.testing, [id]: false } }));
    }
  };
  return {
    data: null,
    defaults: null,
    loading: false,
    error: null,
    testing: {},
    testResults: {},
    receive: (data) => {
      revision++;
      set({ data });
      void get().fetchDefaults();
    },
    fetchDefaults: async () => {
      const sequence = ++defaultsSequence;
      try {
        const defaults = await rtClient.request(Actions.PROVIDERS_DEFAULTS, {});
        if (sequence === defaultsSequence) set({ defaults });
      } catch (err) {
        if (sequence === defaultsSequence) set({ error: toActionError(err) });
      }
    },
    setModels: async (owner_id, models) => {
      const result = await run(() =>
        rtClient.request(Actions.PROVIDERS_SET_MODELS, { owner_id, models })
      );
      await Promise.all([get().list(), get().fetchDefaults()]);
      return result;
    },
    discoverBuiltinModels: async (id) => {
      const result = await run(() =>
        rtClient.request(Actions.PROVIDERS_DISCOVER_BUILTIN_MODELS, { id })
      );
      await Promise.all([get().list(), get().fetchDefaults()]);
      return result;
    },
    updateSettings: async (owner_id, settings) => {
      const result = await run(() =>
        rtClient.request(Actions.PROVIDERS_UPDATE_SETTINGS, { owner_id, settings })
      );
      await get().list();
      return result;
    },
    setDefault: async (purpose, model) => {
      const sequence = ++defaultsSequence;
      const defaults = await run(() =>
        rtClient.request(Actions.PROVIDERS_SET_DEFAULT, { purpose, model })
      );
      if (sequence === defaultsSequence) set({ defaults });
      await get().fetchDefaults();
    },
    list: async () => {
      const sequence = ++listSequence;
      const snapshot = revision;
      set({ loading: true, error: null });
      try {
        const data = await rtClient.request(Actions.PROVIDERS_LIST, {});
        if (sequence === listSequence && snapshot === revision) set({ data });
      } catch (err) {
        if (sequence === listSequence) set({ error: toActionError(err) });
      } finally {
        if (sequence === listSequence) set({ loading: false });
      }
    },
    createCredential: (provider_id, credential) =>
      mutate(() =>
        rtClient.request(Actions.PROVIDERS_CREATE_CREDENTIAL, { provider_id, credential })
      ),
    updateCredential: (id, credential) =>
      mutate(() => rtClient.request(Actions.PROVIDERS_UPDATE_CREDENTIAL, { id, credential }), id),
    deleteCredential: (id) =>
      mutate(() => rtClient.request(Actions.PROVIDERS_DELETE_CREDENTIAL, { id }), id),
    activateCredential: (id) =>
      mutate(() => rtClient.request(Actions.PROVIDERS_ACTIVATE_CREDENTIAL, { id })),
    testCredential: (id) => test(id, Actions.PROVIDERS_TEST_CREDENTIAL),
    createCustom: (provider) =>
      mutate(() => rtClient.request(Actions.PROVIDERS_CREATE_CUSTOM, provider)),
    updateCustom: (id, provider) =>
      mutate(() => rtClient.request(Actions.PROVIDERS_UPDATE_CUSTOM, { id, provider }), id),
    deleteCustom: (id) =>
      mutate(() => rtClient.request(Actions.PROVIDERS_DELETE_CUSTOM, { id }), id),
    testCustom: (id) => test(id, Actions.PROVIDERS_TEST_CUSTOM),
    discoverModels: (payload) =>
      run(() => rtClient.request(Actions.PROVIDERS_DISCOVER_MODELS, payload)),
  };
});
