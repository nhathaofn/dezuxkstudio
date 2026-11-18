import React, { useState } from "react";
import { Pause, Play, RefreshCw, RotateCcw, ShieldCheck, XCircle } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { Switch } from "../../components/ui/Switch";
import { StudioJobKindValues } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentStore } from "../agent/useAgentStore";
import { formatUSD } from "./studioFormatters";
import { useStudioStore } from "./useStudioStore";

export const StudioQueueCostDrawer: React.FC = () => {
  const t = useT();
  const open = useStudioStore((s) => s.queueDrawerOpen);
  const openQueueDrawer = useStudioStore((s) => s.openQueueDrawer);
  const projDetail = useStudioStore((s) => s.projectDetail);
  const activeEpId = useStudioStore((s) => s.activeEpisodeId);
  const jobs = useStudioStore((s) => s.jobs);
  const costSummary = useStudioStore((s) => s.costSummary);
  const controlQueue = useStudioStore((s) => s.controlQueue);
  const fetchQueue = useStudioStore((s) => s.fetchQueue);
  const fetchCostSummary = useStudioStore((s) => s.fetchCostSummary);
  const updateProject = useStudioStore((s) => s.updateProject);
  const upsertPricing = useStudioStore((s) => s.upsertPricing);
  const enqueueBatch = useStudioStore((s) => s.enqueueBatch);
  const saving = useStudioStore((s) => s.saving);

  const approvals = useAgentStore((s) => s.approvals);
  const resolveApproval = useAgentStore((s) => s.resolveApproval);
  const studioApprovals = approvals.filter((a) => a.tool.startsWith("studio_"));

  const [priceEdits, setPriceEdits] = useState<Record<string, string>>({});

  if (!open || !projDetail) return null;
  const proj = projDetail.project;

  return (
    <Dialog title={t("studio.queue.title")} size="lg" onClose={() => openQueueDrawer(false)}>
      <div className="studio-phase2-stack">
        <div className="studio-approval-bar">
          <div>
            <strong>{t("studio.approval.gateLabel")}</strong>
            <div className="studio-field-hint">
              {proj.auto_approve_batches
                ? t("studio.approval.gateAuto")
                : t("studio.approval.gateRequired")}
            </div>
          </div>
          <Switch
            checked={Boolean(proj.auto_approve_batches)}
            onCheckedChange={(checked) =>
              void updateProject({ id: proj.id, auto_approve_batches: checked })
            }
            aria-label={t("studio.approval.gateLabel")}
          />
        </div>

        {studioApprovals.length > 0 && (
          <div className="studio-qc-box">
            <strong>{t("studio.approval.pendingTitle")}</strong>
            {studioApprovals.map((ap) => (
              <div key={ap.id} className="studio-approval-row">
                <div>
                  <div>
                    <strong>{ap.title}</strong>
                  </div>
                  <div className="studio-field-hint">{ap.summary}</div>
                </div>
                <div className="studio-phase2-meta-grid">
                  <Button
                    size="sm"
                    variant="primary"
                    onClick={() => void resolveApproval(ap.id, true)}
                  >
                    <ShieldCheck size={13} />
                    {t("studio.approval.approve")}
                  </Button>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => void resolveApproval(ap.id, false)}
                  >
                    {t("studio.approval.reject")}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="studio-phase2-toolbar">
          {activeEpId && (
            <Button
              size="sm"
              variant="primary"
              disabled={saving}
              onClick={() => void enqueueBatch({ stage: "generate_videos" })}
            >
              <Play size={13} />
              {t("studio.queue.enqueueBatch")}
            </Button>
          )}
          <Button
            size="sm"
            variant="secondary"
            disabled={saving}
            onClick={() =>
              void controlQueue({
                action: "pause",
                project_id: proj.id,
                episode_id: activeEpId ?? undefined,
              })
            }
          >
            <Pause size={13} />
            {t("studio.queue.pauseAll")}
          </Button>
          <Button
            size="sm"
            variant="secondary"
            disabled={saving}
            onClick={() =>
              void controlQueue({
                action: "resume",
                project_id: proj.id,
                episode_id: activeEpId ?? undefined,
              })
            }
          >
            <Play size={13} />
            {t("studio.queue.resumeAll")}
          </Button>
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              void fetchQueue(proj.id, activeEpId ?? undefined);
              void fetchCostSummary(proj.id, activeEpId ?? undefined);
            }}
          >
            <RefreshCw size={13} />
            {t("studio.queue.refresh")}
          </Button>
        </div>

        {jobs.length === 0 ? (
          <p className="studio-field-hint">{t("studio.queue.empty")}</p>
        ) : (
          <div className="studio-queue-list">
            {jobs.map((job) => (
              <div key={job.id} className="studio-queue-item">
                <div className="studio-queue-item__main">
                  <div className="studio-phase2-meta-grid">
                    <strong>
                      {(StudioJobKindValues as readonly string[]).includes(job.job_kind)
                        ? t(`studio.queue.kind.${job.job_kind}` as never)
                        : job.job_kind}
                    </strong>
                    <Badge
                      className={
                        job.status === "succeeded"
                          ? "studio-badge--ok"
                          : job.status === "failed"
                            ? "studio-badge--error"
                            : "studio-badge--info"
                      }
                    >
                      {t(`studio.queue.status.${job.status}` as never)}
                    </Badge>
                    <span>{job.progress}%</span>
                    <span>{formatUSD(job.actual_usd || job.estimated_usd, 4)}</span>
                  </div>
                  {job.provider_job_id && (
                    <div className="studio-field-hint">
                      {t("studio.queue.providerId", { id: job.provider_job_id })}
                    </div>
                  )}
                  {job.error_text && (
                    <div className="studio-notice studio-notice--error">{job.error_text}</div>
                  )}
                </div>
                <div className="studio-phase2-meta-grid">
                  {(job.status === "queued" ||
                    job.status === "polling" ||
                    job.status === "submitted") && (
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => void controlQueue({ action: "pause", job_id: job.id })}
                    >
                      {t("studio.queue.pause")}
                    </Button>
                  )}
                  {job.status === "paused" && (
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => void controlQueue({ action: "resume", job_id: job.id })}
                    >
                      {t("studio.queue.resume")}
                    </Button>
                  )}
                  {job.status === "failed" && (
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => void controlQueue({ action: "retry", job_id: job.id })}
                    >
                      <RotateCcw size={12} />
                      {t("studio.queue.retry")}
                    </Button>
                  )}
                  {job.status !== "succeeded" && job.status !== "canceled" && (
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => void controlQueue({ action: "cancel", job_id: job.id })}
                    >
                      <XCircle size={12} />
                      {t("studio.queue.cancel")}
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="studio-cost-section">
          <strong>{t("studio.cost.title")}</strong>
          {costSummary ? (
            <div className="studio-phase2-stack">
              <div className="studio-phase2-meta-grid">
                <Badge className="studio-badge--info">
                  {t("studio.cost.actualTotal")}: {formatUSD(costSummary.actual_total_usd, 4)}
                </Badge>
                <Badge>
                  {t("studio.cost.estimatedTotal")}: {formatUSD(costSummary.estimated_total_usd, 4)}
                </Badge>
                <Badge>{t("studio.cost.entriesCount", { count: costSummary.entry_count })}</Badge>
              </div>
              {Object.keys(costSummary.by_capability ?? {}).length > 0 && (
                <div className="studio-phase2-meta-grid">
                  {Object.entries(costSummary.by_capability ?? {}).map(([cap, usd]) => (
                    <Badge key={cap}>
                      {cap}: {formatUSD(usd, 4)}
                    </Badge>
                  ))}
                </div>
              )}
              {(costSummary.pricing ?? []).length > 0 && (
                <details className="studio-pricing-details">
                  <summary>{t("studio.cost.pricingCatalog")}</summary>
                  <div className="studio-pricing-list">
                    {costSummary.pricing.map((pr) => {
                      const val = priceEdits[pr.id] ?? String(pr.unit_price_usd);
                      return (
                        <div key={pr.id} className="studio-pricing-row">
                          <span>
                            {pr.capability} · {pr.provider_id}/{pr.model_id} ({pr.unit})
                          </span>
                          <div className="studio-phase2-meta-grid">
                            <input
                              type="number"
                              step="0.001"
                              min={0}
                              className="studio-price-input"
                              value={val}
                              onChange={(e) =>
                                setPriceEdits((prev) => ({ ...prev, [pr.id]: e.target.value }))
                              }
                            />
                            <Button
                              size="sm"
                              variant="secondary"
                              onClick={() =>
                                void upsertPricing({
                                  provider_id: pr.provider_id,
                                  model_id: pr.model_id,
                                  capability: pr.capability,
                                  unit: pr.unit,
                                  unit_price_usd: Math.max(0, Number(val) || 0),
                                })
                              }
                            >
                              {t("studio.cost.savePrice")}
                            </Button>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </details>
              )}
            </div>
          ) : (
            <p className="studio-field-hint">{t("studio.cost.empty")}</p>
          )}
        </div>
      </div>
    </Dialog>
  );
};
