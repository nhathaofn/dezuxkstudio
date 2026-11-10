import React from "react";
import { RefreshCw, Wrench } from "lucide-react";
import type { BinaryInfo } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { useConnectionStore } from "../connection/useConnectionStore";
import { useBinariesStore } from "./useBinariesStore";
import { BinaryCard } from "./BinaryCard";

export const BinariesView: React.FC = () => {
  const t = useT();
  const connected = useConnectionStore((s) => s.status === "connected");

  const binaries = useBinariesStore((s) => s.binaries);
  const loading = useBinariesStore((s) => s.loading);
  const error = useBinariesStore((s) => s.error);
  const fetchBinaries = useBinariesStore((s) => s.fetchBinaries);
  const probe = useBinariesStore((s) => s.probe);
  const download = useBinariesStore((s) => s.download);
  const setCustomPath = useBinariesStore((s) => s.setCustomPath);

  return (
    <div className="cards-stack flex-full-height">
      <Card
        icon={Wrench}
        title={t("tools.title")}
        subtitle={t("tools.subtitle")}
        wide
        className="card--flex-fill"
        actions={
          <Button
            onClick={() => void fetchBinaries()}
            disabled={!connected || loading}
            title={t("tools.refresh")}
          >
            <RefreshCw size={13} className={loading ? "animate-spin" : ""} />
            {t("tools.refresh")}
          </Button>
        }
      >
        {error && <ErrorText error={error} />}

        <div className="scrollable-pane">
          {binaries.length === 0 ? (
            <div className="empty-state">
              <Wrench size={28} className="empty-state__icon" />
              <div className="empty-state__title">
                {loading
                  ? t("app.loading")
                  : connected
                    ? t("tools.status.missing")
                    : t("status.connecting")}
              </div>
            </div>
          ) : (
            <div className="tools-binary-list">
              {binaries.map((bin: BinaryInfo) => (
                <BinaryCard
                  key={bin.id}
                  binary={bin}
                  onProbe={probe}
                  onDownload={download}
                  onSetCustomPath={setCustomPath}
                  disabled={!connected}
                />
              ))}
            </div>
          )}
        </div>
      </Card>
    </div>
  );
};

export default BinariesView;
