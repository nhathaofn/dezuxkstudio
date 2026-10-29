import React, { useState } from "react";
import { Cpu, Folder, RefreshCw } from "lucide-react";
import { FolderTargetValues, type FolderTarget } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";
import { useSystemStore } from "./useSystemStore";

const formatUptime = (sec: number): string => {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return h > 0 ? `${h}h ${m}m` : `${m}m ${sec % 60}s`;
};

export const SystemCard: React.FC = () => {
  const t = useT();
  const info = useSystemStore((s) => s.info);
  const infoError = useSystemStore((s) => s.infoError);
  const loadInfo = useSystemStore((s) => s.loadInfo);
  const openFolder = useSystemStore((s) => s.openFolder);
  const [openError, setOpenError] = useState<ActionError | null>(null);

  const open = async (target: FolderTarget) => setOpenError(await openFolder(target));

  return (
    <Card
      icon={Cpu}
      title={t("system.title")}
      actions={
        <Button
          onClick={() => void loadInfo()}
          title={t("system.refresh")}
          aria-label={t("system.refresh")}
        >
          <RefreshCw size={13} />
        </Button>
      }
    >
      {info && (
        <dl className="kv-grid">
          <dt>{t("system.version")}</dt>
          <dd>
            {info.build.version} <span className="muted-text">({info.build.commit})</span>
          </dd>
          <dt>{t("system.mode")}</dt>
          <dd>{info.mode}</dd>
          <dt>{t("system.platform")}</dt>
          <dd>
            {info.build.os}/{info.build.arch} · {info.build.go_version}
          </dd>
          <dt>{t("system.uptime")}</dt>
          <dd>{formatUptime(info.runtime.uptime_seconds)}</dd>
        </dl>
      )}
      <div className="row row--wrap">
        {FolderTargetValues.map((target) => (
          <Button key={target} onClick={() => void open(target)}>
            <Folder size={13} /> {t(`system.open.${target}`)}
          </Button>
        ))}
      </div>
      <ErrorText error={infoError ?? openError} />
    </Card>
  );
};
