import { useState } from "react";
import { RefreshCw } from "lucide-react";
import type { BuiltinProvider } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { mergeDiscovered } from "./customModels";
import { ModelList } from "./ModelList";
import { DiscoveryError } from "./DiscoveryError";
import { useProvidersStore } from "./useProvidersStore";

export function BuiltinModels({
  provider,
  disabled,
}: {
  provider: BuiltinProvider;
  disabled: boolean;
}) {
  const t = useT();
  const save = useProvidersStore((s) => s.setModels);
  const discover = useProvidersStore((s) => s.discoverBuiltinModels);
  const [draft, setDraft] = useState<typeof provider.models | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ActionError | null>(null);
  const [saved, setSaved] = useState(false);
  const models = draft ?? provider.models;
  return (
    <details className="providers-details">
      <summary>{t("providers.modelsCount", { count: models.length })}</summary>
      <form
        className="providers-form"
        onSubmit={async (e) => {
          e.preventDefault();
          if (busy || disabled) return;
          setBusy(true);
          setError(null);
          setSaved(false);
          try {
            await save(provider.id, models);
            setDraft(null);
            setSaved(true);
          } catch (err) {
            setError(toActionError(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <DiscoveryError error={error} />
        {provider.discoverable ? (
          <Button
            type="button"
            disabled={busy || disabled}
            onClick={async () => {
              setBusy(true);
              setError(null);
              setSaved(false);
              try {
                const result = await discover(provider.id);
                setDraft((previous) =>
                  previous ? mergeDiscovered(previous, result.models) : result.models
                );
              } catch (err) {
                setError(toActionError(err));
              } finally {
                setBusy(false);
              }
            }}
          >
            <RefreshCw size={14} />
            {t(busy ? "providers.discovering" : "providers.refreshModels")}
          </Button>
        ) : (
          <p className="subtitle">{t("providers.noDiscovery")}</p>
        )}
        <ModelList
          models={models}
          onChange={(rows) => {
            setDraft(rows);
            setSaved(false);
          }}
          capabilities={provider.capabilities}
          disabled={busy || disabled}
          error={error}
        />
        <Button type="submit" variant="primary" disabled={busy || disabled}>
          {t(busy ? "providers.saving" : "providers.save")}
        </Button>
        {saved && (
          <p role="status" className="providers-success">
            {t("providers.saved")}
          </p>
        )}
      </form>
    </details>
  );
}
