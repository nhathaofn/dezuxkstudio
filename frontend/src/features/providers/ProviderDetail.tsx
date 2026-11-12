import { useId, useState } from "react";
import { ArrowUpRight, Play, Plus } from "lucide-react";
import type { CustomEndpoint, ProviderCredential } from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { openExternal } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { BuiltinModels } from "./BuiltinModels";
import { CustomProviderForm } from "./CustomProviderForm";
import { CapabilityIcon } from "./CapabilityIcon";
import { HealthBadge } from "./HealthBadge";
import { fieldLabel } from "./fieldLabel";
import { ProviderLimits } from "./ProviderLimits";
import { ProviderActions, TestResult } from "./ProviderActions";
import { activeHealth, providerName, providerStatus, type ProviderEntry } from "./layout";
import { useProvidersStore } from "./useProvidersStore";

export function ProviderDetail({
  entry,
  disabled,
  endpoints,
  onAdd,
  onEditCredential,
  onEditCustom,
  onSaved,
}: {
  entry: ProviderEntry;
  disabled: boolean;
  endpoints: CustomEndpoint[];
  onAdd: () => void;
  onEditCredential: (credential: ProviderCredential) => void;
  onEditCustom: () => void;
  onSaved: () => void;
}) {
  const t = useT();
  const id = useId();
  const [tab, setTab] = useState<"key" | "model" | "limits">("key");
  const activate = useProvidersStore((s) => s.activateCredential);
  const test = useProvidersStore((s) => s.testCredential);
  const remove = useProvidersStore((s) => s.deleteCredential);
  const testCustom = useProvidersStore((s) => s.testCustom);
  const removeCustom = useProvidersStore((s) => s.deleteCustom);
  const active =
    entry.kind === "builtin" ? entry.provider.credentials.find((key) => key.active) : undefined;
  const testId =
    entry.kind === "custom"
      ? entry.provider.key_hint
        ? entry.provider.id
        : undefined
      : active?.id;
  const testing = useProvidersStore((s) => (testId ? s.testing[testId] : false));
  const health = activeHealth(entry);
  const status = providerStatus(entry);
  const provider = entry.provider;
  const tabLabel = (value: typeof tab) =>
    value === "key"
      ? entry.kind === "custom"
        ? "providers.connectionTab"
        : "providers.keyTab"
      : value === "model"
        ? "providers.modelTab"
        : "providers.limits";
  return (
    <>
      <header className="providers-detail-header">
        <div className="providers-heading">
          <span className="providers-avatar providers-avatar--large">
            {providerName(entry).charAt(0).toLocaleUpperCase()}
          </span>
          <h2 title={providerName(entry)} className="providers-truncate">
            {providerName(entry)}
          </h2>
          <span
            className={`providers-status providers-status--${status}`}
            title={
              health?.tested
                ? [t(`providers.outcome.${health.outcome}`), health.detail]
                    .filter(Boolean)
                    .join(": ")
                : undefined
            }
          >
            <span className={`providers-dot providers-dot--${status}`} />
            {t(`providers.status.${status}`)}
          </span>
          <div className="providers-capabilities">
            {provider.capabilities.map((capability) => (
              <span key={capability}>
                <CapabilityIcon value={capability} />
                {t(`providers.capability.${capability}`)}
              </span>
            ))}
          </div>
          {entry.kind === "builtin" && entry.provider.docs_url && (
            <button
              type="button"
              className="providers-docs providers-text-button"
              onClick={() => openExternal(entry.provider.docs_url)}
            >
              {t("providers.getApiKey")} <ArrowUpRight size={12} />
            </button>
          )}
        </div>
        <div className="card-actions-row">
          <Button
            type="button"
            variant={testId ? "secondary" : "primary"}
            disabled={disabled || testing}
            onClick={() => {
              if (!testId) {
                if (entry.kind === "custom") onEditCustom();
                else onAdd();
                return;
              }
              void (entry.kind === "custom" ? testCustom(testId) : test(testId)).catch(() => {
                /* Store renders the action error. */
              });
            }}
          >
            {testId ? <Play size={14} /> : <Plus size={14} />}
            {t(
              testId
                ? testing
                  ? "providers.testing"
                  : "providers.testConnection"
                : "providers.addApiKey"
            )}
          </Button>
        </div>
      </header>
      <div className="providers-detail-tabs" role="tablist" aria-label={providerName(entry)}>
        {(["key", "model", "limits"] as const).map((value) => (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={tab === value}
            aria-controls={`${id}-${value}-panel`}
            id={`${id}-${value}`}
            onClick={() => setTab(value)}
          >
            {t(tabLabel(value))}
            {value === "key" && entry.kind === "builtin" && (
              <span className="providers-tab-count">{entry.provider.credentials.length}</span>
            )}
            {value === "model" && (
              <span className="providers-tab-count">
                {provider.models.filter((model) => model.enabled).length}/{provider.models.length}
              </span>
            )}
          </button>
        ))}
      </div>
      <section
        role="tabpanel"
        hidden={tab !== "key"}
        id={`${id}-key-panel`}
        aria-labelledby={`${id}-key`}
      >
        {entry.kind === "builtin" ? (
          <>
            {!entry.provider.credentials.length && (
              <p className="subtitle">{t("providers.noCredentials")}</p>
            )}
            {entry.provider.credentials.map((credential) => (
              <div key={credential.id} className="providers-credential">
                <div className="providers-credential-row">
                  <div className="providers-credential-info">
                    <strong className="providers-truncate" title={credential.name}>
                      {credential.name === providerName(entry)
                        ? t("providers.apiKey")
                        : credential.name}
                    </strong>
                    {credential.active && (
                      <Badge className="providers-active">{t("providers.active")}</Badge>
                    )}
                    <div className="providers-key-values">
                      {Object.entries({ ...credential.values, ...credential.secret_hints }).map(
                        ([key, value]) => (
                          <code
                            key={key}
                            className="providers-truncate"
                            title={`${fieldLabel(t, key)}: ${value}`}
                          >
                            {value}
                          </code>
                        )
                      )}
                    </div>
                  </div>
                  <HealthBadge health={credential.health} />
                  <ProviderActions
                    id={credential.id}
                    disabled={disabled}
                    active={credential.active}
                    onActivate={() => activate(credential.id)}
                    onTest={() => test(credential.id)}
                    onEdit={() => onEditCredential(credential)}
                    onDelete={() => remove(credential.id)}
                  />
                </div>
                <TestResult id={credential.id} />
              </div>
            ))}
            <Button type="button" variant="ghost" disabled={disabled} onClick={onAdd}>
              <Plus size={14} />
              {t("providers.addKey")}
            </Button>
          </>
        ) : (
          <>
            <dl className="providers-values">
              <div>
                <dt>{t("providers.field.base_url")}</dt>
                <dd>
                  <code title={entry.provider.base_url}>{entry.provider.base_url}</code>
                </dd>
              </div>
              <div>
                <dt>{t("providers.protocol")}</dt>
                <dd>{t(`providers.protocol.${entry.provider.discovery_format}`)}</dd>
              </div>
              <div>
                <dt>{t("providers.field.api_key")}</dt>
                <dd>
                  <code>{entry.provider.key_hint || t("providers.noKey")}</code>
                </dd>
              </div>
            </dl>
            <HealthBadge health={entry.provider.health} />
            <ProviderActions
              id={provider.id}
              disabled={disabled}
              onTest={() => testCustom(provider.id)}
              onEdit={onEditCustom}
              onDelete={() => removeCustom(provider.id)}
            />
            <TestResult id={provider.id} />
          </>
        )}
      </section>
      <section
        role="tabpanel"
        hidden={tab !== "model"}
        id={`${id}-model-panel`}
        aria-labelledby={`${id}-model`}
      >
        {entry.kind === "builtin" ? (
          <BuiltinModels provider={entry.provider} disabled={disabled} />
        ) : (
          <CustomProviderForm
            key={JSON.stringify([
              entry.provider.name,
              entry.provider.base_url,
              entry.provider.discovery_format,
              entry.provider.key_hint,
              entry.provider.models,
            ])}
            provider={entry.provider}
            endpoints={endpoints}
            disabled={disabled}
            onClose={() => {}}
            onSaved={onSaved}
            modelsOnly
          />
        )}
      </section>
      <section
        role="tabpanel"
        hidden={tab !== "limits"}
        id={`${id}-limits-panel`}
        aria-labelledby={`${id}-limits`}
      >
        <ProviderLimits
          ownerId={provider.id}
          capabilities={
            entry.kind === "custom"
              ? entry.provider.models.map((model) => model.capability)
              : provider.capabilities
          }
          settings={provider.settings}
          defaults={provider.setting_defaults}
          disabled={disabled}
        />
      </section>
    </>
  );
}
