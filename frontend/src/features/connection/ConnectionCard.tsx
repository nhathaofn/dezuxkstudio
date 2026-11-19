import React, { useState } from "react";
import { Check, Edit2, Eye, EyeOff, KeyRound, RotateCcw, Server, Trash2, X } from "lucide-react";
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
  const setRemoteToken = useConnectionStore((s) => s.setRemoteToken);

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [invalid, setInvalid] = useState(false);

  const [editingToken, setEditingToken] = useState(false);
  const [tokenDraft, setTokenDraft] = useState("");
  const [showToken, setShowToken] = useState(false);
  const [tokenBusy, setTokenBusy] = useState(false);

  // In the desktop shell the endpoint comes from the bridge and cannot be edited (except client-mode token).
  const managed = endpoint?.source === "bridge";
  const canEditToken = endpoint?.mode === "client" || !managed;

  const apply = () => {
    const url = draft.trim();
    if (!isValidWsUrl(url)) {
      setInvalid(true);
      return;
    }
    setEditing(false);
    void setManualUrl(url);
  };

  const applyToken = async () => {
    setTokenBusy(true);
    try {
      await setRemoteToken(tokenDraft);
      setEditingToken(false);
      setTokenDraft("");
      setShowToken(false);
    } finally {
      setTokenBusy(false);
    }
  };

  const clearToken = async () => {
    setTokenBusy(true);
    try {
      await setRemoteToken("");
      setEditingToken(false);
      setTokenDraft("");
      setShowToken(false);
    } finally {
      setTokenBusy(false);
    }
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

      {canEditToken && (
        <div style={{ marginTop: "var(--space-3)" }}>
          <label className="field-label">{t("connection.token")}</label>
          {!editingToken ? (
            <div className="url-display">
              <span className="muted-text">
                {endpoint?.token ? t("connection.tokenConfigured") : t("connection.tokenUnset")}
              </span>
              <div className="row">
                <Button
                  onClick={() => {
                    setTokenDraft(endpoint?.token ?? "");
                    setShowToken(false);
                    setEditingToken(true);
                  }}
                >
                  <KeyRound size={13} /> {t("connection.tokenSet")}
                </Button>
                {endpoint?.token && (
                  <Button disabled={tokenBusy} onClick={() => void clearToken()}>
                    <Trash2 size={13} /> {t("connection.tokenClear")}
                  </Button>
                )}
              </div>
            </div>
          ) : (
            <div className="url-edit">
              <input
                type={showToken ? "text" : "password"}
                className="input-control"
                value={tokenDraft}
                onChange={(e) => setTokenDraft(e.target.value)}
                placeholder={t("connection.tokenPlaceholder")}
                autoComplete="new-password"
                spellCheck={false}
              />
              <Button
                iconOnly
                title={showToken ? t("providers.credential.hide") : t("providers.credential.show")}
                aria-label={
                  showToken ? t("providers.credential.hide") : t("providers.credential.show")
                }
                onClick={() => setShowToken((v) => !v)}
              >
                {showToken ? <EyeOff size={13} /> : <Eye size={13} />}
              </Button>
              <Button variant="primary" disabled={tokenBusy} onClick={() => void applyToken()}>
                <Check size={14} /> {t("connection.save")}
              </Button>
              <Button disabled={tokenBusy} onClick={() => setEditingToken(false)}>
                <X size={14} /> {t("connection.cancel")}
              </Button>
            </div>
          )}
        </div>
      )}
    </Card>
  );
};
