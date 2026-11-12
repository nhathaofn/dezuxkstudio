import { useId, useRef, useState } from "react";
import { Plus, Search, Server, Trash2 } from "lucide-react";
import {
  ProviderDiscoveryFormatValues,
  type CustomEndpoint,
  type CustomModelInput,
  type CustomProvider,
  type ProviderDiscoveryFormat,
} from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { appConfig } from "../../config/appConfig";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { reconcileModels } from "./customModels";
import { SecretInput } from "./SecretInput";
import { useProvidersStore } from "./useProvidersStore";

interface CustomProviderFormProps {
  provider?: CustomProvider;
  endpoints: CustomEndpoint[];
  onClose: () => void;
  onSaved: () => void;
  disabled: boolean;
}
type ModelRow = CustomModelInput & { rowId: number };

export function CustomProviderForm({
  provider,
  endpoints,
  onClose,
  onSaved,
  disabled,
}: CustomProviderFormProps) {
  const t = useT();
  const id = useId();
  const create = useProvidersStore((s) => s.createCustom);
  const update = useProvidersStore((s) => s.updateCustom);
  const discover = useProvidersStore((s) => s.discoverModels);
  const [name, setName] = useState(provider?.name ?? "");
  const [protocol, setProtocol] = useState<ProviderDiscoveryFormat>(
    provider?.discovery_format ?? ProviderDiscoveryFormatValues[0]
  );
  const [baseUrl, setBaseUrl] = useState(provider?.base_url ?? "");
  const [apiKey, setApiKey] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const [models, setModels] = useState<ModelRow[]>(() =>
    (provider?.models ?? []).map((model, rowId) => ({ ...model, rowId }))
  );
  const nextRow = useRef(models.length);
  const [error, setError] = useState<ActionError | null>(null);
  const [saving, setSaving] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [discoveredCount, setDiscoveredCount] = useState<number | null>(null);
  const [preset, setPreset] = useState("");
  const allowed = endpoints.filter((endpoint) => endpoint.formats.includes(protocol));
  const busy = saving || discovering;
  const invalid = (field: string) => error?.details?.field === field;
  const setFormat = (format: ProviderDiscoveryFormat) => {
    setProtocol(format);
    setModels((previous) =>
      reconcileModels(previous, endpoints, format).map((model, index) => ({
        ...model,
        rowId: previous[index].rowId,
      }))
    );
    setDiscoveredCount(null);
  };
  const changeModel = (index: number, patch: Partial<CustomModelInput>) => {
    setModels((previous) => {
      const updated = previous.map((row, rowIndex) =>
        rowIndex === index ? { ...row, ...patch } : row
      );
      const capability = endpoints.find(
        (endpoint) => endpoint.key === updated[index].endpoint
      )?.capability;
      if (patch.is_default) {
        for (let rowIndex = 0; rowIndex < updated.length; rowIndex++) {
          if (
            rowIndex !== index &&
            endpoints.find((endpoint) => endpoint.key === updated[rowIndex].endpoint)
              ?.capability === capability
          )
            updated[rowIndex] = { ...updated[rowIndex], is_default: false };
        }
      }
      return reconcileModels(updated, endpoints, protocol).map((model, rowIndex) => ({
        ...model,
        rowId: updated[rowIndex].rowId,
      }));
    });
  };
  return (
    <Card icon={Server} title={t(provider ? "providers.editCustom" : "providers.addCustom")}>
      <form
        className="providers-form"
        onSubmit={async (e) => {
          e.preventDefault();
          if (busy || disabled) return;
          setSaving(true);
          setError(null);
          try {
            const payload = {
              name: name.trim(),
              discovery_format: protocol,
              base_url: baseUrl.trim(),
              api_key: apiKey,
              clear_api_key: clearKey,
              models: models.map(({ model_id, endpoint, enabled, is_default }) => ({
                model_id: model_id.trim(),
                endpoint,
                enabled,
                is_default,
              })),
            };
            if (provider) await update(provider.id, payload);
            else await create(payload);
            onSaved();
          } catch (err) {
            setError(toActionError(err));
          } finally {
            setSaving(false);
          }
        }}
      >
        <ErrorText error={error} />
        <fieldset className="providers-fieldset" disabled={busy || disabled}>
          <div className="providers-form-grid">
            <div className="form-group">
              <label className="field-label" htmlFor={`${id}-preset`}>
                {t("providers.preset")}
              </label>
              <select
                id={`${id}-preset`}
                className="input-control"
                value={preset}
                onChange={(e) => {
                  setPreset(e.target.value);
                  const selected = appConfig.providerPresets.find(
                    (item) => item.name === e.target.value
                  );
                  if (selected) {
                    setName(selected.name);
                    setBaseUrl(selected.baseUrl);
                    setFormat(selected.protocol);
                    setError(null);
                  }
                }}
              >
                <option value="">{t("providers.choosePreset")}</option>
                {appConfig.providerPresets.map((item) => (
                  <option key={item.name} value={item.name}>
                    {item.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="form-group">
              <label className="field-label" htmlFor={`${id}-name`}>
                {t("providers.name")}
              </label>
              <input
                id={`${id}-name`}
                className="input-control"
                value={name}
                required
                aria-invalid={invalid("name")}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="form-group">
              <label className="field-label" htmlFor={`${id}-protocol`}>
                {t("providers.protocol")}
              </label>
              <select
                id={`${id}-protocol`}
                className="input-control"
                value={protocol}
                aria-invalid={invalid("discovery_format")}
                onChange={(e) => setFormat(e.target.value as ProviderDiscoveryFormat)}
              >
                {ProviderDiscoveryFormatValues.map((format) => (
                  <option key={format} value={format}>
                    {t(`providers.protocol.${format}`)}
                  </option>
                ))}
              </select>
            </div>
            <div className="form-group">
              <label className="field-label" htmlFor={`${id}-url`}>
                {t("providers.field.base_url")}
              </label>
              <input
                id={`${id}-url`}
                type="url"
                className="input-control"
                value={baseUrl}
                required
                aria-invalid={invalid("base_url")}
                onChange={(e) => {
                  setBaseUrl(e.target.value);
                  setDiscoveredCount(null);
                }}
              />
            </div>
          </div>
          <SecretInput
            label={t("providers.field.api_key")}
            value={apiKey}
            hint={provider?.key_hint}
            cleared={clearKey}
            invalid={invalid("api_key")}
            onChange={(value) => {
              setApiKey(value);
              setClearKey(false);
            }}
            onClear={() => {
              setClearKey(!clearKey);
              setApiKey("");
            }}
          />
          <div className="card-actions-row">
            <Button
              type="button"
              disabled={!baseUrl.trim()}
              onClick={async () => {
                setDiscovering(true);
                setError(null);
                setDiscoveredCount(null);
                try {
                  const result = await discover({
                    custom_id: clearKey ? "" : (provider?.id ?? ""),
                    discovery_format: protocol,
                    base_url: baseUrl.trim(),
                    api_key: apiKey,
                  });
                  const newRows = result.models
                    .filter((model) => allowed.some((entry) => entry.key === model.endpoint))
                    .map((model) => ({
                      model_id: model.model_id,
                      endpoint: model.endpoint,
                      enabled: true,
                      is_default: false,
                      rowId: nextRow.current++,
                    }));
                  setModels((previous) => {
                    const seen = new Set(
                      previous.map((model) => `${model.model_id}\0${model.endpoint}`)
                    );
                    return [
                      ...previous,
                      ...newRows.filter((model) => {
                        const key = `${model.model_id}\0${model.endpoint}`;
                        if (seen.has(key)) return false;
                        seen.add(key);
                        return true;
                      }),
                    ];
                  });
                  setDiscoveredCount(result.models.length);
                } catch (err) {
                  setError(toActionError(err));
                } finally {
                  setDiscovering(false);
                }
              }}
            >
              <Search size={14} />
              {t(discovering ? "providers.discovering" : "providers.discoverModels")}
            </Button>
            <Button
              type="button"
              disabled={!allowed.length}
              onClick={() => {
                setModels((previous) => [
                  ...previous,
                  {
                    rowId: nextRow.current++,
                    model_id: "",
                    endpoint: allowed[0].key,
                    enabled: true,
                    is_default: false,
                  },
                ]);
              }}
            >
              <Plus size={14} />
              {t("providers.addModel")}
            </Button>
          </div>
          {discoveredCount !== null && (
            <p role="status" className="subtitle">
              {t("providers.discoveredCount", { count: discoveredCount })}
            </p>
          )}
          <div
            className="providers-models"
            aria-invalid={invalid("models")}
            tabIndex={0}
            role="region"
            aria-label={t("providers.models")}
          >
            <table className="providers-model-table">
              <thead>
                <tr>
                  <th>{t("providers.modelId")}</th>
                  <th>{t("providers.endpoint")}</th>
                  <th>{t("providers.enabled")}</th>
                  <th>{t("providers.default")}</th>
                  <th>{t("providers.actions")}</th>
                </tr>
              </thead>
              <tbody>
                {models.map((model, index) => {
                  const capability = endpoints.find(
                    (endpoint) => endpoint.key === model.endpoint
                  )?.capability;
                  return (
                    <tr key={model.rowId}>
                      <td>
                        <input
                          className="input-control input-control--mono"
                          value={model.model_id}
                          required
                          aria-label={t("providers.modelIdRow", { row: index + 1 })}
                          onChange={(e) => changeModel(index, { model_id: e.target.value })}
                        />
                      </td>
                      <td>
                        <select
                          className="input-control"
                          value={model.endpoint}
                          required
                          aria-label={t("providers.endpointRow", { row: index + 1 })}
                          onChange={(e) =>
                            changeModel(index, {
                              endpoint: e.target.value as CustomModelInput["endpoint"],
                            })
                          }
                        >
                          {allowed.map((entry) => (
                            <option key={entry.key} value={entry.key}>
                              {t("providers.endpointOption", {
                                endpoint: entry.key,
                                capability: t(`providers.capability.${entry.capability}`),
                              })}
                            </option>
                          ))}
                        </select>
                      </td>
                      <td>
                        <input
                          type="checkbox"
                          checked={model.enabled}
                          aria-label={t("providers.enabledRow", { row: index + 1 })}
                          onChange={(e) => changeModel(index, { enabled: e.target.checked })}
                        />
                      </td>
                      <td>
                        <input
                          type="radio"
                          name={`${id}-${capability}`}
                          checked={model.is_default}
                          disabled={!model.enabled}
                          aria-label={t("providers.defaultRow", { row: index + 1 })}
                          onChange={() => changeModel(index, { is_default: true })}
                        />
                      </td>
                      <td>
                        <Button
                          type="button"
                          iconOnly
                          title={t("providers.removeModel")}
                          aria-label={t("providers.removeModelRow", { row: index + 1 })}
                          onClick={() =>
                            setModels((previous) =>
                              previous.filter((row) => row.rowId !== model.rowId)
                            )
                          }
                        >
                          <Trash2 size={14} />
                        </Button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {models.length === 0 && <p className="subtitle">{t("providers.noModels")}</p>}
          </div>
        </fieldset>
        <div className="card-actions-row">
          <Button type="submit" variant="primary" disabled={busy || disabled || !allowed.length}>
            {t(saving ? "providers.saving" : "providers.save")}
          </Button>
          <Button type="button" disabled={busy} onClick={onClose}>
            {t("providers.cancel")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
