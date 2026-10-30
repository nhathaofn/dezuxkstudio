import React from "react";
import { Terminal, Trash2 } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { VirtualList } from "../../components/ui/VirtualList";
import { useT } from "../../lib/i18n";
import { useLogStore, type LogEntry } from "./useLogStore";

const ROW_HEIGHT_PX = 28;

export const LiveLogView: React.FC = () => {
  const t = useT();
  const entries = useLogStore((s) => s.entries);
  const clear = useLogStore((s) => s.clear);

  return (
    <div className="cards-stack flex-full-height">
      <Card
        wide
        className="card--flex-fill"
        icon={Terminal}
        title={t("logs.title")}
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
            height="100%"
            className="flex-full-height"
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
