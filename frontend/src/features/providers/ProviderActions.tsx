import { useState } from "react";
import { Check, ChevronRight, Pencil, Play, Trash2 } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { appConfig } from "../../config/appConfig";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { showsTestDetail } from "./health";
import { useProvidersStore } from "./useProvidersStore";

export function TestResult({ id }: { id: string }) {
  const t = useT();
  const result = useProvidersStore((s) => s.testResults[id]);
  if (!result) return null;
  const shown = result.models.slice(0, appConfig.ui.providerTestModelLimit);
  return (
    <div className="providers-test-result" role="status">
      <p className="providers-test-summary">
        <span className={`providers-dot providers-dot--${result.ok ? "ok" : "dead"}`} />
        <span>
          {t(`providers.outcome.${result.outcome}`)}
          {result.ok && <> · {t("providers.latency", { ms: result.latency_ms })}</>}
        </span>
      </p>
      {!result.ok && (result.status > 0 || (result.detail && showsTestDetail(result.outcome))) && (
        <p className="providers-test-error">
          {result.status > 0 && <>{t("providers.httpStatus", { status: result.status })} </>}
          {showsTestDetail(result.outcome) && result.detail}
        </p>
      )}
      {result.ok && shown.length > 0 && (
        <details>
          <summary>
            <ChevronRight size={12} aria-hidden="true" />
            {t("providers.testModels", { count: shown.length })}
          </summary>
          <ul className="providers-test-models">
            {shown.map((model) => (
              <li key={model}>
                <code title={model}>{model}</code>
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
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
            variant="ghost"
            title={t("providers.activate")}
            aria-label={t("providers.activate")}
            disabled={disabled || busy || testing}
            onClick={() => void run(onActivate)}
          >
            <Check size={14} />
            {t("providers.use")}
          </Button>
        )}
        <Button
          type="button"
          variant="ghost"
          title={t(testing ? "providers.testing" : "providers.test")}
          aria-label={t(testing ? "providers.testing" : "providers.test")}
          disabled={disabled || busy || testing}
          onClick={() => void run(onTest)}
        >
          <Play size={14} />
          {t(testing ? "providers.testing" : "providers.test")}
        </Button>
        <Button
          type="button"
          variant="ghost"
          title={t("providers.edit")}
          aria-label={t("providers.edit")}
          disabled={disabled || busy || testing}
          onClick={onEdit}
        >
          <Pencil size={14} />
          {t("providers.edit")}
        </Button>
        <Button
          type="button"
          iconOnly
          variant="ghost"
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
          size="sm"
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
