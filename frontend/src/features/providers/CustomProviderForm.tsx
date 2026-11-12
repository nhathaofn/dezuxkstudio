import { useId, useState } from "react";
import { RefreshCw } from "lucide-react";
import {
  ProviderDiscoveryFormatValues,
  type CustomEndpoint,
  type ProviderModel,
  type CustomProvider,
  type ProviderDiscoveryFormat,
} from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { appConfig } from "../../config/appConfig";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { mergeDiscovered, reconcileModels } from "./customModels";
import { ModelList } from "./ModelList";
import { DiscoveryError } from "./DiscoveryError";
import { SecretInput } from "./SecretInput";
import { useProvidersStore } from "./useProvidersStore";

interface CustomProviderFormProps {
  provider?: CustomProvider;
  endpoints: CustomEndpoint[];
  onClose: () => void;
  onSaved: () => void;
  disabled: boolean;
  /** Reuse the same model editor and discovery/save logic in the detail tab. */
  modelsOnly?: boolean;
}

export function CustomProviderForm({
  provider,
  endpoints,
  onClose,
  onSaved,
  disabled,
  modelsOnly = false,
}: CustomProviderFormProps) {
  const t = useT();
  const id = useId();
  const create = useProvidersStore((s) => s.createCustom);
  const update = useProvidersStore((s) => s.updateCustom);
  const discover = useProvidersStore((s) => s.discoverModels);
  const saveModels = useProvidersStore((s) => s.setModels);
  const [name, setName] = useState(provider?.name ?? "");
  const [protocol, setProtocol] = useState<ProviderDiscoveryFormat>(
    provider?.discovery_format ?? ProviderDiscoveryFormatValues[0]
  );
  const [baseUrl, setBaseUrl] = useState(provider?.base_url ?? "");
  const [apiKey, setApiKey] = useState("");
  const [clearKey, setClearKey] = useState(false);
  const [models, setModels] = useState<ProviderModel[]>(provider?.models ?? []);
  const [error, setError] = useState<ActionError | null>(null);
  const [saving, setSaving] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const [discoveredCount, setDiscoveredCount] = useState<number | null>(null);
  const [preset, setPreset] = useState("");
  const allowed = endpoints.filter((endpoint) => endpoint.formats.includes(protocol));
  const busy = saving || discovering;
  const [initial] = useState(() =>
    JSON.stringify({ name, protocol, baseUrl, apiKey, clearKey, models })
  );
  const dirty = initial !== JSON.stringify({ name, protocol, baseUrl, apiKey, clearKey, models });
  const invalid = (field: string) => error?.details?.field === field;
  const setFormat = (format: ProviderDiscoveryFormat) => {
    setProtocol(format);
    setModels((previous) => reconcileModels(previous, endpoints, format));
    setDiscoveredCount(null);
  };
  const form = (
    <form
      id={`${id}-form`}
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
            models: models.map((model) => ({
              ...model,
              model_id: model.model_id.trim(),
              display_name: model.display_name.trim(),
            })),
          };
          if (modelsOnly && provider) await saveModels(provider.id, payload.models);
          else if (provider) await update(provider.id, payload);
          else await create(payload);
          onSaved();
        } catch (err) {
          setError(toActionError(err));
        } finally {
          setSaving(false);
        }
      }}
    >
      <DiscoveryError error={error} />
      <fieldset className="providers-fieldset" disabled={busy || disabled}>
        {!modelsOnly && (
          <>
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
          </>
        )}
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
                setModels((previous) =>
                  mergeDiscovered(
                    previous,
                    result.models.filter((model) =>
                      allowed.some((entry) => entry.key === model.endpoint)
                    )
                  )
                );
                setDiscoveredCount(result.models.length);
              } catch (err) {
                setError(toActionError(err));
              } finally {
                setDiscovering(false);
              }
            }}
          >
            <RefreshCw size={14} />
            {t(discovering ? "providers.discovering" : "providers.refreshModels")}
          </Button>
        </div>
        {discoveredCount !== null && (
          <p role="status" className="subtitle">
            {t("providers.discoveredCount", { count: discoveredCount })}
          </p>
        )}
        <ModelList
          models={models}
          onChange={setModels}
          capabilities={allowed.map((entry) => entry.capability)}
          endpoints={allowed}
          error={error}
          disabled={busy || disabled}
        />
      </fieldset>
      {modelsOnly && (
        <div className="card-actions-row">
          <Button type="submit" variant="primary" disabled={busy || disabled || !allowed.length}>
            {t(saving ? "providers.saving" : "providers.save")}
          </Button>
        </div>
      )}
    </form>
  );
  if (modelsOnly) return form;
  return (
    <Dialog
      title={t(provider ? "providers.editCustom" : "providers.addCustom")}
      onClose={onClose}
      dirty={dirty}
      busy={busy}
      footer={(requestClose) => (
        <>
          <Button type="button" disabled={busy} onClick={requestClose}>
            {t("providers.cancel")}
          </Button>
          <Button
            type="submit"
            form={`${id}-form`}
            variant="primary"
            disabled={busy || disabled || !allowed.length}
          >
            {t(saving ? "providers.saving" : "providers.save")}
          </Button>
        </>
      )}
    >
      {form}
    </Dialog>
  );
}
