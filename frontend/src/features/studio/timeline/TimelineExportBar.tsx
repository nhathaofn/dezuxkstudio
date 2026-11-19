import React from "react";
import { Bot, Download, Film, FolderOpen } from "lucide-react";
import { Badge } from "../../../components/ui/Badge";
import { Button } from "../../../components/ui/Button";
import { Card } from "../../../components/ui/Card";
import type {
  ExportNLEDraftOutput,
  StudioTimelineRenderResult,
} from "../../../contracts/generated";
import { isMessageKey, useT } from "../../../lib/i18n";
import { mediaBasename, saveMediaToDevice } from "../../../lib/rtClient";
import { askAgentAboutMediaAsset } from "../../agent/screenContext";
import { SignedStudioVideo } from "../SignedStudioMedia";

export interface TimelineExportBarProps {
  finalVideoPath: string | undefined;
  finalVideoAssetId: string | undefined;
  lastRenderResult: StudioTimelineRenderResult | null;
  lastNLEExport: ExportNLEDraftOutput | null;
  openExportFolder: (path?: string) => Promise<unknown>;
}

export const TimelineExportBar: React.FC<TimelineExportBarProps> = ({
  finalVideoPath,
  finalVideoAssetId,
  lastRenderResult,
  lastNLEExport,
  openExportFolder,
}) => {
  const t = useT();
  return (
    <Card
      icon={Film}
      title={t("studio.timeline.exportsTitle")}
      actions={
        <Button
          variant="ghost"
          size="sm"
          onClick={() => void openExportFolder(finalVideoPath || lastNLEExport?.export_path)}
        >
          <FolderOpen size={14} />
          {t("studio.export.openFolder")}
        </Button>
      }
    >
      <div className="timeline-export-summary">
        <div className="timeline-export-artifact">
          <div className="timeline-track-title">
            <span>{t("studio.timeline.finalMP4Card")}</span>
            {lastRenderResult?.duration_sec ? (
              <Badge>
                {t("studio.common.secondsShort", {
                  sec: lastRenderResult.duration_sec.toFixed(1),
                })}
              </Badge>
            ) : null}
          </div>
          <div className="studio-media-frame studio-media-frame--export">
            <SignedStudioVideo
              assetId={finalVideoAssetId}
              placeholder={t("studio.export.noOutput")}
              large
            />
          </div>
          {finalVideoPath && (
            <div className="studio-toolbar">
              <Button
                variant="secondary"
                size="sm"
                onClick={() =>
                  void saveMediaToDevice(
                    finalVideoPath,
                    mediaBasename(finalVideoPath),
                    undefined,
                    finalVideoAssetId
                  )
                }
              >
                <Download size={14} />
                {t("studio.export.download")}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  askAgentAboutMediaAsset({
                    assetId: finalVideoAssetId,
                    filePath: finalVideoPath,
                    mimeType: "video/mp4",
                  })
                }
              >
                <Bot size={14} />
                {t("studio.common.askAgent")}
              </Button>
            </div>
          )}
        </div>

        {lastNLEExport &&
          (() => {
            const nleBadgeKey = `studio.timeline.nleBadge.${lastNLEExport.format}`;
            return (
              <div className="timeline-export-artifact">
                <div className="timeline-track-title">
                  <span>{t("studio.timeline.nlePackageReady")}</span>
                  <Badge>
                    {isMessageKey(nleBadgeKey)
                      ? t(nleBadgeKey)
                      : lastNLEExport.format.toUpperCase()}
                  </Badge>
                </div>
                <p>{mediaBasename(lastNLEExport.export_path)}</p>
                <div className="studio-toolbar">
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() =>
                      void saveMediaToDevice(
                        lastNLEExport.export_path,
                        mediaBasename(lastNLEExport.export_path),
                        undefined,
                        lastNLEExport.asset_id
                      )
                    }
                  >
                    <Download size={14} />
                    {t("studio.timeline.downloadNLE")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => void openExportFolder(lastNLEExport.export_path)}
                  >
                    <FolderOpen size={14} />
                    {t("studio.export.openFolder")}
                  </Button>
                </div>
              </div>
            );
          })()}
      </div>
    </Card>
  );
};
