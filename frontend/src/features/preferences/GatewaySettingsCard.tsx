import React, { useState } from "react";
import { Server } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { useT } from "../../lib/i18n";
import { usePreferencesStore } from "./usePreferencesStore";

export const GatewaySettingsCard: React.FC = () => {
  const t = useT();
  const prefs = usePreferencesStore((s) => s.prefs);
  const update = usePreferencesStore((s) => s.update);

  const [gatewayUrlDraft, setGatewayUrlDraft] = useState<string | null>(null);
  const [gatewayApiKeyDraft, setGatewayApiKeyDraft] = useState<string | null>(null);

  const effectiveGatewayUrl = gatewayUrlDraft ?? prefs.gatewayUrl ?? "http://127.0.0.1:8080";
  const effectiveGatewayApiKey = gatewayApiKeyDraft ?? "";

  const isUrlModified = gatewayUrlDraft !== null && gatewayUrlDraft !== prefs.gatewayUrl;
  const isKeyModified = gatewayApiKeyDraft !== null && gatewayApiKeyDraft !== "";

  const handleSaveUrl = () => {
    if (isUrlModified) {
      void update({ gatewayUrl: effectiveGatewayUrl });
      setGatewayUrlDraft(null);
    }
  };

  const handleSaveApiKey = () => {
    if (isKeyModified && gatewayApiKeyDraft !== null) {
      void update({ gatewayApiKey: gatewayApiKeyDraft });
      setGatewayApiKeyDraft(null);
    }
  };

  return (
    <Card
      icon={Server}
      title={t("settings.gateway.title")}
      subtitle={t("settings.gateway.desc")}
      className="card--flex-fill"
    >
      <div className="settings-rows-list">
        <div
          className="settings-row"
          style={{
            flexDirection: "column",
            alignItems: "stretch",
            gap: "var(--space-2)",
          }}
        >
          <div className="settings-row-info">
            <span className="settings-row-title">{t("settings.gateway.endpoint")}</span>
            <span className="settings-row-desc">{t("settings.gateway.endpointDesc")}</span>
          </div>
          <div style={{ display: "flex", gap: "var(--space-2)" }}>
            <input
              type="text"
              className="input-control"
              value={effectiveGatewayUrl}
              onChange={(e) => setGatewayUrlDraft(e.target.value)}
              onBlur={handleSaveUrl}
              placeholder="http://127.0.0.1:8080"
              aria-label={t("settings.gateway.endpoint")}
            />
            {isUrlModified && (
              <Button size="sm" variant="primary" onClick={handleSaveUrl}>
                {t("settings.gateway.save")}
              </Button>
            )}
          </div>
        </div>

        <div
          className="settings-row"
          style={{
            flexDirection: "column",
            alignItems: "stretch",
            gap: "var(--space-2)",
          }}
        >
          <div className="settings-row-info">
            <span className="settings-row-title">{t("settings.gateway.apiKey")}</span>
            <span className="settings-row-desc">{t("settings.gateway.apiKeyDesc")}</span>
          </div>
          <div style={{ display: "flex", gap: "var(--space-2)" }}>
            <input
              type="password"
              className="input-control"
              value={effectiveGatewayApiKey}
              onChange={(e) => setGatewayApiKeyDraft(e.target.value)}
              onBlur={handleSaveApiKey}
              placeholder={
                prefs.hasGatewayApiKey
                  ? "•••••••• (Configured — enter new key to replace)"
                  : "Bearer token (optional)"
              }
              aria-label={t("settings.gateway.apiKey")}
            />
            {isKeyModified && (
              <Button size="sm" variant="primary" onClick={handleSaveApiKey}>
                {t("settings.gateway.save")}
              </Button>
            )}
          </div>
        </div>
      </div>
    </Card>
  );
};

export default GatewaySettingsCard;
