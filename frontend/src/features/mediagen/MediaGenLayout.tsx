import React from "react";
import { Loader2, RotateCcw, type LucideIcon } from "lucide-react";
import type { ActionProgress } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";
import { MediaResultCard, type MediaOutputSummary, type MediaResultKind } from "./MediaResultCard";

const QUICK_ASPECT_RATIOS = ["1:1", "16:9", "9:16", "4:3"] as const;

export interface MediaGenLayoutProps {
  kind: MediaResultKind;
  title: string;
  subtitle: string;
  heroMeta?: React.ReactNode;
  settingsIcon: LucideIcon;
  actionIcon: LucideIcon;
  actionLabel: string;
  loading: boolean;
  canRun: boolean;
  hasResult: boolean;
  onGenerate: () => void;
  onReset: () => void;
  children: React.ReactNode;
  progress: ActionProgress | null;
  error: ActionError | null;
  prompt?: string;
  sourcePath?: string;
  result: MediaOutputSummary | null;
  downloadingPath: string | null;
  onDownload: () => void;
  onRetry?: () => void;
  onDismissError: () => void;
}

export const MediaGenLayout: React.FC<MediaGenLayoutProps> = ({
  kind,
  title,
  subtitle,
  heroMeta,
  settingsIcon,
  actionIcon: ActionIcon,
  actionLabel,
  loading,
  canRun,
  hasResult,
  onGenerate,
  onReset,
  children,
  progress,
  error,
  prompt,
  sourcePath,
  result,
  downloadingPath,
  onDownload,
  onRetry,
  onDismissError,
}) => {
  const t = useT();

  return (
    <div className="dashboard-layout watermark-layout">
      <div className="dashboard-hero watermark-hero">
        <div className="watermark-hero-title-group">
          <h1 className="dashboard-hero-title watermark-hero-title">{title}</h1>
          <div className="dashboard-hero-meta">
            <span>{subtitle}</span>
            {heroMeta}
          </div>
        </div>

        <div className="watermark-hero-actions">
          <Button variant="ghost" size="sm" onClick={onReset} disabled={loading && !hasResult}>
            <RotateCcw size={13} />
            {t("watermark.resetBtn")}
          </Button>
          <Button variant="primary" size="sm" onClick={onGenerate} disabled={!canRun}>
            {loading ? <Loader2 size={13} className="animate-spin" /> : <ActionIcon size={13} />}
            {loading ? t("mediagen.action.generating") : actionLabel}
          </Button>
        </div>
      </div>

      <div className="watermark-grid">
        <Card icon={settingsIcon} title={t("mediagen.card.settings")} className="card--flex-fill">
          <div className="watermark-config-stack">{children}</div>
        </Card>

        <MediaResultCard
          kind={kind}
          loading={loading}
          progress={progress}
          error={error}
          prompt={prompt}
          sourcePath={sourcePath}
          result={result}
          downloadingPath={downloadingPath}
          onDownload={onDownload}
          onRetry={onRetry}
          onDismissError={onDismissError}
        />
      </div>
    </div>
  );
};

interface AspectRatioPresetsProps {
  options: readonly string[];
  active: string;
  disabled?: boolean;
  onSelect: (ratio: string) => void;
}

export const AspectRatioPresets: React.FC<AspectRatioPresetsProps> = ({
  options,
  active,
  disabled,
  onSelect,
}) => {
  const presets = QUICK_ASPECT_RATIOS.filter((ratio) => options.includes(ratio));
  if (presets.length === 0) return null;

  return (
    <div
      className="mediagen-aspect-presets"
      style={{
        display: "flex",
        gap: "var(--space-1)",
        marginTop: "var(--space-1)",
        flexWrap: "wrap",
      }}
    >
      {presets.map((ratio) => (
        <Button
          key={ratio}
          type="button"
          size="sm"
          variant={active === ratio ? "primary" : "secondary"}
          disabled={disabled}
          onClick={() => onSelect(ratio)}
        >
          {ratio}
        </Button>
      ))}
    </div>
  );
};
