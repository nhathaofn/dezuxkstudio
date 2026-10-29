import React, { useState } from "react";
import { Check, Edit2, RotateCcw, Server, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { useT } from "../../lib/i18n";
import { isValidWsUrl } from "../../lib/rtClient";
import { useConnectionStore } from "./useConnectionStore";

export const ConnectionCard: React.FC = () => {
  const t = useT();
  const endpoint = useConnectionStore((s) => s.endpoint);
  const setManualUrl = useConnectionStore((s) => s.setManualUrl);
  const resetToAuto = useConnectionStore((s) => s.resetToAuto);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [invalid, setInvalid] = useState(false);

  // In the desktop shell the endpoint and token come from the bridge and cannot be edited.
  const managed = endpoint?.source === "bridge";

  const apply = () => {
    const url = draft.trim();
    if (!isValidWsUrl(url)) {
      setInvalid(true);
      return;
    }
    setEditing(false);
    void setManualUrl(url);
  };

  return (
    <Card icon={Server} title={t("connection.title")} subtitle={t("connection.subtitle")}>
      <label className="field-label">{t("connection.endpoint")}</label>
      {!editing ? (
        <div className="url-display">
          <code>{endpoint?.url || t("connection.resolving")}</code>
          {managed ? (
            <span className="muted-text">{t("connection.managed")}</span>
          ) : (
            <div className="row">
              <Button
                onClick={() => {
                  setDraft(endpoint?.url ?? "");
                  setInvalid(false);
                  setEditing(true);
                }}
              >
                <Edit2 size={13} /> {t("connection.edit")}
              </Button>
              <Button onClick={() => void resetToAuto()}>
                <RotateCcw size={13} /> {t("connection.auto")}
              </Button>
            </div>
          )}
        </div>
      ) : (
        <>
          <div className="url-edit">
            <input
              className="input-control"
              value={draft}
              onChange={(e) => {
                setDraft(e.target.value);
                setInvalid(false);
              }}
              placeholder="ws://host:port/ws"
              aria-invalid={invalid}
            />
            <Button variant="primary" onClick={apply}>
              <Check size={14} /> {t("connection.save")}
            </Button>
            <Button onClick={() => setEditing(false)}>
              <X size={14} /> {t("connection.cancel")}
            </Button>
          </div>
          {invalid && <div className="alert-box">{t("connection.invalidUrl")}</div>}
        </>
      )}
    </Card>
  );
};
