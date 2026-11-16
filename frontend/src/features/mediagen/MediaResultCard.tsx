import React, { useEffect, useRef } from "react";
import { useInRouterContext, useNavigate } from "react-router-dom";
import { CheckCircle2, FileAudio, Film, Image as ImageIcon } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Card } from "../../components/ui/Card";
import type { ActionProgress } from "../../contracts/generated";
import type { ActionError } from "../../lib/rtClient";
import { useT } from "../../lib/i18n";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { formatByteSize } from "./types";
import {
  MediaOutputNode,
  AudioWaveformPlayer,
  type MediaOutputItem,
} from "../../components/ui/MediaOutputNode";

export { AudioWaveformPlayer };
export type MediaResultKind = "image" | "video" | "tts";

export interface MediaOutputSummary {
  path: string;
  mimeType: string;
  provider: string;
  model: string;
  sizeBytes: number;
  duration?: number;
  asset_id?: string;
}

export interface MediaResultCardProps {
  kind: MediaResultKind;
  loading: boolean;
  progress: ActionProgress | null;
  error: ActionError | null;
  result: MediaOutputSummary | null;
  prompt?: string | null;
  sourcePath?: string | null;
  downloadingPath: string | null;
  onDownload: () => void;
  onRetry?: () => void;
  onDismissError?: () => void;
}

const RouterNavigateBridge: React.FC<{
  navRef: React.MutableRefObject<((path: string, opts?: { state?: unknown }) => void) | null>;
}> = ({ navRef }) => {
  const navigate = useNavigate();
  useEffect(() => {
    navRef.current = navigate;
  }, [navigate, navRef]);
  return null;
};

export const MediaResultCard: React.FC<MediaResultCardProps> = ({
  kind,
  loading,
  progress,
  error,
  result,
  prompt,
  sourcePath,
  downloadingPath,
  onDownload,
  onRetry,
  onDismissError,
}) => {
  const t = useT();
  const inRouter = useInRouterContext();
  const navRef = useRef<((path: string, opts?: { state?: unknown }) => void) | null>(null);

  const KindIcon = kind === "video" ? Film : kind === "tts" ? FileAudio : ImageIcon;

  const handleSendToWatermark = (filePath: string) => {
    if (!filePath) return;
    if (navRef.current) {
      navRef.current("/watermark", { state: { asset_id: result?.asset_id, path: filePath } });
    }
  };

  const handleAskAgent = (meta: { assetId?: string; filePath: string; mimeType?: string }) => {
    askAgentAboutMediaAsset({
      assetId: meta.assetId,
      filePath: meta.filePath,
      mimeType: meta.mimeType,
    });
  };

  const item: MediaOutputItem | null = result
    ? {
        outputPath: result.path,
        mimeType: result.mimeType,
        provider: result.provider,
        model: result.model,
        sizeBytes: result.sizeBytes,
        duration: result.duration,
        asset_id: result.asset_id,
        prompt: prompt,
        sourcePath: sourcePath,
      }
    : null;

  return (
    <Card
      icon={KindIcon}
      title={t("mediagen.card.output")}
      className="card--flex-fill"
      actions={
        result ? (
          <Badge className="watermark-badge-connected">
            <CheckCircle2 size={11} className="watermark-badge-icon" />{" "}
            {formatByteSize(result.sizeBytes)}
          </Badge>
        ) : undefined
      }
    >
      {inRouter && <RouterNavigateBridge navRef={navRef} />}

      <MediaOutputNode
        kind={kind}
        item={item}
        loading={loading}
        progress={progress}
        error={error}
        downloading={Boolean(result && downloadingPath === result.path)}
        onDownload={onDownload}
        onRetry={onRetry}
        onDismissError={onDismissError}
        onSendToWatermark={kind === "image" ? handleSendToWatermark : undefined}
        onAskAgent={handleAskAgent}
      />
    </Card>
  );
};

export default MediaResultCard;
