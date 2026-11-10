import React, { useState } from "react";
import { Cpu, Folder, RefreshCw } from "lucide-react";
import { FolderTargetValues, type FolderTarget } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";
import { useSystemStore } from "./useSystemStore";

export const SystemCard: React.FC = () => {
  const t = useT();
  const info = useSystemStore((s) => s.info);
  const infoError = useSystemStore((s) => s.infoError);
  const loadInfo = useSystemStore((s) => s.loadInfo);
  const openFolder = useSystemStore((s) => s.openFolder);
  const [openError, setOpenError] = useState<ActionError | null>(null);

  const formatUptime = (sec: number): string => {
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    return h > 0 ? t("system.uptime.hm", { h, m }) : t("system.uptime.ms", { m, s: sec % 60 });
  };

  const open = async (target: FolderTarget) => setOpenError(await openFolder(target));

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
        <dl className="kv-grid">
          <dt>{t("system.version")}</dt>
          <dd>
            {info ? (
              <>
                {info.build.version} <span className="muted-text">({info.build.commit})</span>
              </>
            ) : (
              <span className="muted-text">—</span>
            )}
          </dd>
          <dt>{t("system.mode")}</dt>
          <dd>{info ? info.mode : <span className="muted-text">—</span>}</dd>
          <dt>{t("system.platform")}</dt>
          <dd>
            {info ? (
              `${info.build.os}/${info.build.arch} · ${info.build.go_version}`
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
        <div className="system-folder-buttons">
          {FolderTargetValues.map((target) => (
            <Button key={target} size="sm" onClick={() => void open(target)}>
              <Folder size={12} /> {t(`system.open.${target}`)}
            </Button>
          ))}
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
