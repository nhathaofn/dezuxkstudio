import React, { useEffect, useState } from "react";
import { Cpu, Folder, RefreshCw } from "lucide-react";
import { FolderTargetValues, type FolderTarget } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { isDesktop } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";
import { useSystemStore } from "./useSystemStore";
import { formatDuration } from "../../lib/format";

export const SystemCard: React.FC = () => {
  const t = useT();
  const info = useSystemStore((s) => s.info) ?? useSystemStore.getState().info;
  const buildInfo = useSystemStore((s) => s.buildInfo) ?? useSystemStore.getState().buildInfo;
  const infoError = useSystemStore((s) => s.infoError);
  const loadInfo = useSystemStore((s) => s.loadInfo);
  const loadBuildInfo = useSystemStore((s) => s.loadBuildInfo);
  const openFolder = useSystemStore((s) => s.openFolder);
  const [openError, setOpenError] = useState<ActionError | null>(null);

  useEffect(() => {
    if (isDesktop() && !buildInfo) {
      void loadBuildInfo();
    }
  }, [buildInfo, loadBuildInfo]);

  const effectiveBuild = info?.build ?? buildInfo;
  const isRemote = info?.mode === "server" || info?.mode === "client";
  const desktopOnlyTooltip = t("system.desktopOnly");

  const formatUptime = (sec: number): string => formatDuration(t, sec);

  const open = async (target: FolderTarget) => {
    if (isRemote) return;
    setOpenError(await openFolder(target));
  };

  return (
    <Card
      icon={Cpu}
      title={t("system.title")}
      className="card--flex-fill"
      actions={
        <Button
          size="sm"
          iconOnly
          onClick={() => void loadInfo()}
          title={t("system.refresh")}
          aria-label={t("system.refresh")}
        >
          <RefreshCw size={13} />
        </Button>
      }
    >
      <div className="system-card-content">
        <div className="system-specs-box">
          <dl className="kv-grid">
            <dt>{t("system.version")}</dt>
            <dd>
              {effectiveBuild ? (
                <>
                  {effectiveBuild.version} <span className="muted-text">({effectiveBuild.commit})</span>
                </>
              ) : (
                <span className="muted-text">—</span>
              )}
            </dd>
            <dt>{t("system.mode")}</dt>
            <dd>{info ? info.mode : <span className="muted-text">—</span>}</dd>
            <dt>{t("system.platform")}</dt>
            <dd>
              {effectiveBuild ? (
                `${effectiveBuild.os}/${effectiveBuild.arch} · ${effectiveBuild.go_version}`
              ) : (
                <span className="muted-text">—</span>
              )}
            </dd>
            <dt>{t("system.uptime")}</dt>
            <dd>
              {info ? (
                formatUptime(info.runtime.uptime_seconds)
              ) : (
                <span className="muted-text">—</span>
              )}
            </dd>
          </dl>
        </div>
        <div className="system-folder-section">
          <span className="system-folder-header">{t("system.foldersTitle")}</span>
          <div className="system-folder-buttons">
            {FolderTargetValues.map((target) => (
              <Button
                key={target}
                size="sm"
                disabled={isRemote}
                title={isRemote ? desktopOnlyTooltip : undefined}
                onClick={() => void open(target)}
              >
                <Folder size={12} /> {t(`system.open.${target}`)}
              </Button>
            ))}
          </div>
        </div>
        {(infoError ?? openError) && (
          <div className="system-card-error">
            <ErrorText error={infoError ?? openError} />
          </div>
        )}
      </div>
    </Card>
  );
};
