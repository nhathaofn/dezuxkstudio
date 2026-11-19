import React from "react";
import { ShieldCheck } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { appConfig } from "../../config/appConfig";
import { isMessageKey, translateServerMessage, useT } from "../../lib/i18n";
import { formatUSD } from "./studioFormatters";
import { useStudioStore } from "./useStudioStore";

export const StudioPreflightDialog: React.FC = () => {
  const t = useT();
  const pending = useStudioStore((s) => s.pendingPreflight);
  const cancel = useStudioStore((s) => s.cancelPreflight);
  const confirm = useStudioStore((s) => s.confirmPreflight);
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);
  if (!pending) return null;

  const { preflight } = pending;
  const stageKey = `studio.batchStage.${pending.stage}`;
  const stageLabel = isMessageKey(stageKey) ? t(stageKey) : pending.stage;
  const unitKey = `studio.cost.unit.${preflight.unit}`;
  const unitLabel = isMessageKey(unitKey) ? t(unitKey) : preflight.unit;
  const usdDigits = appConfig.studio.ui.detailUsdDigits;

  return (
    <Dialog
      title={t("studio.approval.preflightTitle")}
      size="sm"
      onClose={cancel}
      footer={
        <div className="studio-dialog-footer">
          <Button variant="secondary" onClick={cancel}>
            {t("dialog.close")}
          </Button>
          <Button variant="primary" disabled={!preflight.ready} onClick={() => void confirm()}>
            <ShieldCheck size={14} />
            {t("studio.approval.confirmRun")}
          </Button>
        </div>
      }
    >
      <div className="studio-phase2-stack">
        <ErrorText error={error} onDismiss={clearError} />
        <div className="studio-phase2-meta-grid">
          <Badge>{stageLabel}</Badge>
          <Badge>{t("studio.approval.preflightItems", { count: preflight.item_count })}</Badge>
          <Badge>
            {t("studio.approval.preflightEst", {
              usd: formatUSD(preflight.estimated_usd, usdDigits),
            })}
          </Badge>
          <Badge>{t("studio.approval.preflightSec", { sec: preflight.estimated_sec })}</Badge>
        </div>
        {preflight.model_id && (
          <div className="studio-field-hint">
            {preflight.provider_id}/{preflight.model_id} ·{" "}
            {t("studio.cost.perUnit", {
              price: formatUSD(preflight.unit_price_usd, usdDigits),
              unit: unitLabel,
            })}
          </div>
        )}
        {preflight.warning_items && preflight.warning_items.length > 0
          ? preflight.warning_items.map((w, idx) => (
              <div key={idx} className="studio-notice studio-notice--warn">
                {translateServerMessage(t, w.msg_key, w.msg_params) ?? w.message}
              </div>
            ))
          : (preflight.warnings ?? []).map((w, idx) => (
              <div key={idx} className="studio-notice studio-notice--warn">
                {w}
              </div>
            ))}
        {preflight.blocker_items && preflight.blocker_items.length > 0
          ? preflight.blocker_items.map((b, idx) => (
              <div key={idx} className="studio-notice studio-notice--error">
                {translateServerMessage(t, b.msg_key, b.msg_params) ?? b.message}
              </div>
            ))
          : (preflight.blockers ?? []).map((b, idx) => (
              <div key={idx} className="studio-notice studio-notice--error">
                {b}
              </div>
            ))}
        {!preflight.ready && (
          <div className="studio-notice studio-notice--error">{t("studio.approval.blocked")}</div>
        )}
      </div>
    </Dialog>
  );
};
