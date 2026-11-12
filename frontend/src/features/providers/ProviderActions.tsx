import { useState } from "react";
import { Check, Pencil, Play, Trash2 } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { appConfig } from "../../config/appConfig";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { useProvidersStore } from "./useProvidersStore";

export function TestResult({ id }: { id: string }) {
  const t = useT();
  const result = useProvidersStore((s) => s.testResults[id]);
  if (!result) return null;
  return (
    <details
      className={`providers-test-result providers-notice--${result.ok ? "success" : "danger"}`}
    >
      <summary>
        {t(`providers.outcome.${result.outcome}`)}
        {result.ok && <> · {t("providers.latency", { ms: result.latency_ms })}</>}
      </summary>
      {result.status > 0 && <span>{t("providers.httpStatus", { status: result.status })}</span>}
      {result.detail && <p>{result.detail}</p>}
      {result.ok && result.models.length > 0 && (
        <details>
          <summary>
            {t("providers.testModels", {
              count: Math.min(result.models.length, appConfig.ui.providerTestModelLimit),
            })}
          </summary>
          <ul>
            {result.models.slice(0, appConfig.ui.providerTestModelLimit).map((model) => (
              <li key={model}>
                <code title={model}>{model}</code>
              </li>
            ))}
          </ul>
        </details>
      )}
    </details>
  );
}

export function ProviderActions({
  id,
  disabled,
  active,
  onActivate,
  onTest,
  onEdit,
  onDelete,
}: {
  id: string;
  disabled: boolean;
  active?: boolean;
  onActivate?: () => Promise<void>;
  onTest: () => Promise<void>;
  onEdit: () => void;
  onDelete: () => Promise<void>;
}) {
  const t = useT();
  const testing = useProvidersStore((s) => s.testing[id]);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ActionError | null>(null);
  const run = async (action: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (err) {
      setError(toActionError(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <div className="providers-row-actions">
        {onActivate && !active && (
          <Button
            type="button"
            iconOnly
            title={t("providers.activate")}
            aria-label={t("providers.activate")}
            disabled={disabled || busy || testing}
            onClick={() => void run(onActivate)}
          >
            <Check size={14} />
          </Button>
        )}
        <Button
          type="button"
          iconOnly
          title={t(testing ? "providers.testing" : "providers.test")}
          aria-label={t(testing ? "providers.testing" : "providers.test")}
          disabled={disabled || busy || testing}
          onClick={() => void run(onTest)}
        >
          <Play size={14} />
        </Button>
        <Button
          type="button"
          iconOnly
          title={t("providers.edit")}
          aria-label={t("providers.edit")}
          disabled={disabled || busy || testing}
          onClick={onEdit}
        >
          <Pencil size={14} />
        </Button>
        <Button
          type="button"
          iconOnly
          className="providers-danger"
          title={t("providers.delete")}
          aria-label={t("providers.delete")}
          disabled={disabled || busy || testing}
          onClick={() => setConfirm(true)}
        >
          <Trash2 size={14} />
        </Button>
      </div>
      {!confirm && <ErrorText error={error} />}
      {confirm && (
        <Dialog
          title={t("providers.confirmDelete")}
          onClose={() => setConfirm(false)}
          busy={busy}
          footer={
            <>
              <Button disabled={busy} onClick={() => setConfirm(false)}>
                {t("providers.cancel")}
              </Button>
              <Button
                variant="primary"
                disabled={disabled || busy}
                onClick={() =>
                  void run(async () => {
                    await onDelete();
                    setConfirm(false);
                  })
                }
              >
                {t("providers.confirmDelete")}
              </Button>
            </>
          }
        >
          <p>{t("providers.deleteWarning")}</p>
          <ErrorText error={error} />
        </Dialog>
      )}
    </>
  );
}
