import React from "react";
import { Terminal, Trash2 } from "lucide-react";
import { appConfig } from "../../config/appConfig";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { VirtualList } from "../../components/ui/VirtualList";
import { useT } from "../../lib/i18n";
import { useLogStore, type LogEntry } from "./useLogStore";

const ROW_HEIGHT_PX = 28;
const LIST_HEIGHT_PX = 320;

export const LiveLogView: React.FC = () => {
  const t = useT();
  const entries = useLogStore((s) => s.entries);
  const clear = useLogStore((s) => s.clear);

  return (
    <div className="cards-stack cards-stack--wide">
      <Card
        wide
        icon={Terminal}
        title={t("logs.title")}
        subtitle={t("logs.subtitle", { max: appConfig.logBufferSize })}
        actions={
          <Button onClick={clear}>
            <Trash2 size={13} /> {t("logs.clear")}
          </Button>
        }
      >
        {entries.length === 0 ? (
          <div className="url-display">
            <code>{t("logs.empty")}</code>
          </div>
        ) : (
          <VirtualList<LogEntry>
            items={entries}
            estimateSize={ROW_HEIGHT_PX}
            height={LIST_HEIGHT_PX}
            getItemKey={(e) => e.id}
            renderItem={(e) => (
              <div className={`log-item log-item--${e.level}`}>
                <span className="log-time">{new Date(e.timestamp).toLocaleTimeString()}</span>
                <span>{e.message}</span>
              </div>
            )}
          />
        )}
      </Card>
    </div>
  );
};

export default LiveLogView;
