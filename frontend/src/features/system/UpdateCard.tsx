import React from "react";
import { DownloadCloud, ExternalLink } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { openExternal } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { useSystemStore } from "./useSystemStore";

export const UpdateCard: React.FC = () => {
  const t = useT();
  const update = useSystemStore((s) => s.update);
  const error = useSystemStore((s) => s.updateError);
  const checking = useSystemStore((s) => s.checkingUpdate);
  const check = useSystemStore((s) => s.checkUpdate);

  return (
    <Card
      icon={DownloadCloud}
      title={t("update.title")}
      actions={
        <Button onClick={() => void check()} disabled={checking}>
          {t("update.check")}
        </Button>
      }
    >
      {update && !update.available && (
        <p className="muted-text">{t("update.upToDate", { version: update.current_version })}</p>
      )}
      {update?.available && (
        <div className="row">
          <strong>{t("update.available", { version: update.latest_version })}</strong>
          {update.release_url && (
            <Button variant="primary" onClick={() => openExternal(update.release_url)}>
              <ExternalLink size={13} /> {t("update.open")}
            </Button>
          )}
        </div>
      )}
      <ErrorText error={error} />
    </Card>
  );
};
