import React from "react";
import { Check, ShieldAlert, ShieldCheck } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { appConfig } from "../../config/appConfig";
import type { StudioQCStatus } from "../../contracts/generated";
import { isMessageKey, translateServerMessage, useT } from "../../lib/i18n";
import { formatUSD } from "./studioFormatters";
import { targetVersionKey, useStudioStore } from "./useStudioStore";

export const StudioVersionDrawer: React.FC = () => {
  const t = useT();
  const target = useStudioStore((s) => s.versionDrawerTarget);
  const openVersionDrawer = useStudioStore((s) => s.openVersionDrawer);
  const versionsByTarget = useStudioStore((s) => s.versionsByTarget);
  const qcByTarget = useStudioStore((s) => s.qcByTarget);
  const selectVersion = useStudioStore((s) => s.selectVersion);
  const runQCReview = useStudioStore((s) => s.runQCReview);
  const saving = useStudioStore((s) => s.saving);
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);

  if (!target) return null;
  const key = targetVersionKey(target.targetKind, target.targetId);
  const versions = versionsByTarget[key] ?? [];
  const latestQC = qcByTarget[key];
  const activeVersion = versions.find((v) => v.is_selected) ?? versions[0];

  const handleRunQC = () => {
    void runQCReview({
      project_id: target.projectId,
      episode_id: target.episodeId,
      target_kind: target.targetKind,
      target_id: target.targetId,
      media_version_id: activeVersion?.id,
      media_path: activeVersion?.media_path || target.mediaPath,
      reference_paths: target.referencePaths,
      prompt_expected: target.promptExpected,
    });
  };

  return (
    <Dialog
      title={`${t("studio.versions.title")} · ${target.label}`}
      size="lg"
      onClose={() => openVersionDrawer(null)}
    >
      <div className="studio-phase2-stack">
        <ErrorText error={error} onDismiss={clearError} />
        <div className="studio-phase2-toolbar">
          <Button
            size="sm"
            variant="secondary"
            disabled={saving || !target.mediaPath}
            onClick={handleRunQC}
          >
            {latestQC?.verdict === ("pass" satisfies StudioQCStatus) ? (
              <ShieldCheck size={14} />
            ) : (
              <ShieldAlert size={14} />
            )}
            {t("studio.qc.button")}
          </Button>
          {latestQC && (
            <div className="studio-phase2-meta-grid">
              <Badge
                className={
                  latestQC.verdict === ("pass" satisfies StudioQCStatus)
                    ? "studio-badge--ok"
                    : latestQC.verdict === ("fail" satisfies StudioQCStatus)
                      ? "studio-badge--error"
                      : "studio-badge--warn"
                }
              >
                {t(`studio.qc.verdict.${latestQC.verdict}` as never)}
              </Badge>
              <Badge>{t("studio.qc.score", { score: latestQC.overall_score.toFixed(1) })}</Badge>
            </div>
          )}
        </div>

        {latestQC ? (
          <div className="studio-qc-box">
            {latestQC.vision_summary && (
              <p className="studio-qc-summary">{latestQC.vision_summary}</p>
            )}
            {(latestQC.issues ?? []).length > 0 && (
              <ul className="studio-qc-issues">
                {(latestQC.issues ?? []).map((iss, idx) => {
                  const sevKey = `studio.qc.severity.${iss.severity}`;
                  const catKey = `studio.qc.category.${iss.category}`;
                  const sevLabel = isMessageKey(sevKey) ? t(sevKey) : iss.severity;
                  const catLabel = isMessageKey(catKey) ? t(catKey) : iss.category;
                  return (
                    <li key={idx}>
                      <strong>
                        {t("studio.qc.issueTag", { severity: sevLabel, category: catLabel })}
                      </strong>{" "}
                      {translateServerMessage(t, iss.msg_key, iss.msg_params) ?? iss.message}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        ) : (
          <p className="studio-field-hint">{t("studio.qc.noReport")}</p>
        )}

        {versions.length === 0 ? (
          <p className="studio-field-hint">{t("studio.versions.empty")}</p>
        ) : (
          <div className="studio-version-grid">
            {versions.map((v) => (
              <div
                key={v.id}
                className={`studio-version-card ${v.is_selected ? "studio-version-card--selected" : ""}`}
              >
                <div className="studio-version-card__head">
                  <strong>{t("studio.versions.versionLabel", { num: v.version_num })}</strong>
                  <div className="studio-phase2-meta-grid">
                    {v.is_selected && (
                      <Badge className="studio-badge--ok">{t("studio.versions.active")}</Badge>
                    )}
                    <Badge>{t(`studio.qc.verdict.${v.qc_status}` as never)}</Badge>
                  </div>
                </div>
                <div className="studio-field-hint">
                  {v.model_ref || v.source_node} ·{" "}
                  {formatUSD(v.cost_usd, appConfig.studio.ui.detailUsdDigits)}
                </div>
                {v.prompt_used && <p className="studio-version-prompt">{v.prompt_used}</p>}
                <div className="studio-field-hint studio-path-truncate">{v.media_path}</div>
                {!v.is_selected && (
                  <Button
                    size="sm"
                    variant="primary"
                    disabled={saving}
                    onClick={() => void selectVersion(v.id)}
                  >
                    <Check size={13} />
                    {t("studio.versions.select")}
                  </Button>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </Dialog>
  );
};
