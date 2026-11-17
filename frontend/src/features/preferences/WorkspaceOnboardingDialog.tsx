import React, { useState } from "react";
import { Folder, FolderPlus } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { getBridge, isDesktop } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { ActionError, toActionError } from "../../lib/rtClient";
import { useSystemStore } from "../system/useSystemStore";
import { usePreferencesStore } from "./usePreferencesStore";

export const WorkspaceOnboardingDialog: React.FC = () => {
  const t = useT();
  const sysInfo = useSystemStore((s) => s.info);
  const updatePrefs = usePreferencesStore((s) => s.update);
  const prefError = usePreferencesStore((s) => s.error);

  const suggestedRoot = sysInfo?.paths?.suggested_workspace_root ?? "";
  const [userPath, setUserPath] = useState<string | null>(null);
  const selectedPath = userPath ?? suggestedRoot;
  const [saving, setSaving] = useState(false);
  const [localError, setLocalError] = useState<ActionError | null>(null);

  const handleBrowse = async () => {
    const bridge = getBridge();
    if (!bridge?.BrowseDirectory) return;
    try {
      const chosen = await bridge.BrowseDirectory(t("onboarding.browseTitle"));
      if (chosen) {
        setUserPath(chosen);
        setLocalError(null);
        if (prefError) usePreferencesStore.setState({ error: null });
      }
    } catch (err) {
      setLocalError(toActionError(err));
    }
  };

  const handleUseDefault = async () => {
    setSaving(true);
    setLocalError(null);
    try {
      await updatePrefs({ workspace_root: "", onboarding_completed: true });
    } catch (err) {
      setLocalError(toActionError(err));
    } finally {
      setSaving(false);
    }
  };

  const handleSave = async () => {
    const path = selectedPath.trim();
    if (!path) {
      setLocalError(new ActionError("ERR_VALIDATION", t("onboarding.error.emptyPath")));
      return;
    }
    setSaving(true);
    setLocalError(null);
    try {
      await updatePrefs({ workspace_root: path, onboarding_completed: true });
    } catch (err) {
      setLocalError(toActionError(err));
    } finally {
      setSaving(false);
    }
  };

  const activeError = localError ?? prefError;
  const displayRoot = selectedPath.trim() || t("onboarding.tree.defaultRoot");

  return (
    <Dialog
      title={t("onboarding.title")}
      busy={saving}
      dismissible={false}
      onClose={() => {}}
      size="md"
      footer={
        <div className="row onboarding-footer">
          <Button
            type="button"
            variant="secondary"
            onClick={() => void handleUseDefault()}
            disabled={saving}
          >
            {t("onboarding.useDefault")}
          </Button>
          <Button
            type="button"
            variant="primary"
            onClick={() => void handleSave()}
            disabled={saving || !selectedPath.trim()}
          >
            {t("onboarding.saveAndStart")}
          </Button>
        </div>
      }
    >
      <div className="stack onboarding-body">
        <p className="onboarding-desc">{t("onboarding.desc")}</p>

        <div className="stack onboarding-input-group">
          <div className="row onboarding-input-row">
            <input
              type="text"
              className="input-control onboarding-input"
              value={selectedPath}
              onChange={(e) => {
                setUserPath(e.target.value);
                setLocalError(null);
                if (prefError) usePreferencesStore.setState({ error: null });
              }}
              placeholder={suggestedRoot || t("onboarding.inputPlaceholder")}
              disabled={saving}
              aria-label={t("onboarding.browseTitle")}
            />
            <Button
              type="button"
              variant="secondary"
              onClick={() => void handleBrowse()}
              disabled={saving || !isDesktop()}
            >
              <FolderPlus size={14} />
              {t("onboarding.browseBtn")}
            </Button>
          </div>

          {activeError && <ErrorText error={activeError} />}
        </div>

        <div className="onboarding-tree-preview">
          <div className="onboarding-tree-header">
            <Folder size={14} />
            <span className="truncate">{displayRoot}</span>
          </div>
          <ul className="onboarding-tree-list">
            <li>
              media/images{" "}
              <span className="onboarding-tree-note">({t("onboarding.tree.images")})</span>
            </li>
            <li>
              media/videos{" "}
              <span className="onboarding-tree-note">({t("onboarding.tree.videos")})</span>
            </li>
            <li>
              media/tts <span className="onboarding-tree-note">({t("onboarding.tree.tts")})</span>
            </li>
            <li>
              media/watermark{" "}
              <span className="onboarding-tree-note">({t("onboarding.tree.watermark")})</span>
            </li>
            <li>
              workspace{" "}
              <span className="onboarding-tree-note">({t("onboarding.tree.workspace")})</span>
            </li>
          </ul>
        </div>
      </div>
    </Dialog>
  );
};
