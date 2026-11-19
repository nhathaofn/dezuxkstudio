import React, { useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { ErrorText } from "../../components/ui/ErrorText";
import { Switch } from "../../components/ui/Switch";
import {
  NodeTypes,
  type StudioGenre,
  type StudioProductionMode,
  type StudioProject,
} from "../../contracts/generated";
import { isMessageKey, useT } from "../../lib/i18n";
import { MediaModelSelect } from "../mediagen/MediaModelSelect";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { useStudioStore } from "./useStudioStore";

export const CreateProjectDialog: React.FC<{
  onClose: () => void;
}> = ({ onClose }) => {
  const t = useT();
  const scriptSpec = useNodeSpec(NodeTypes.SCRIPT_NORMALIZE);
  const shotSpec = useNodeSpec(NodeTypes.AUTHOR_SHOT_PROMPTS);
  const genreParam = findParam(scriptSpec, "genre");
  const genreOptions = (genreParam?.options ?? []) as StudioGenre[];
  const modeParam = findParam(shotSpec, "production_mode");
  const modeOptions = (modeParam?.options ?? []) as StudioProductionMode[];
  const aspectParam = findParam(scriptSpec, "aspect_ratio");
  const aspectOptions = aspectParam?.options ?? [];
  const createProject = useStudioStore((s) => s.createProject);
  const saving = useStudioStore((s) => s.saving);
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);

  const [title, setTitle] = useState("");
  const [genre, setGenre] = useState("");
  const [productionMode, setProductionMode] = useState("");
  const [aspectRatio, setAspectRatio] = useState("");
  const [defaultTtsModel, setDefaultTtsModel] = useState("");
  const [stylePrompt, setStylePrompt] = useState("");
  const [autoApproveBatches, setAutoApproveBatches] = useState(false);

  const effectiveGenre = (genre || genreParam?.default || genreOptions[0] || "") as StudioGenre;
  const effectiveProductionMode = (productionMode ||
    modeParam?.default ||
    modeOptions[0] ||
    "") as StudioProductionMode;
  const effectiveAspectRatio = aspectRatio || aspectParam?.default || aspectOptions[0] || "";

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim()) return;
    const ok = await createProject({
      title: title.trim(),
      ...(effectiveGenre ? { genre: effectiveGenre } : {}),
      ...(effectiveProductionMode ? { production_mode: effectiveProductionMode } : {}),
      ...(effectiveAspectRatio ? { aspect_ratio: effectiveAspectRatio } : {}),
      ...(defaultTtsModel ? { default_tts_model: defaultTtsModel } : {}),
      style_prompt: stylePrompt.trim(),
      auto_approve_batches: autoApproveBatches,
    });
    if (ok) onClose();
  };

  return (
    <Dialog
      title={t("studio.modal.newProject")}
      onClose={onClose}
      busy={saving}
      dirty={title.trim().length > 0 || stylePrompt.trim().length > 0}
      footer={(requestClose) => (
        <>
          <Button variant="ghost" onClick={requestClose} disabled={saving}>
            {t("studio.modal.cancel")}
          </Button>
          <Button
            variant="primary"
            onClick={handleSubmit}
            disabled={saving || title.trim().length === 0}
          >
            {saving ? <Loader2 size={14} className="animate-spin" /> : <Plus size={14} />}
            {t("studio.modal.create")}
          </Button>
        </>
      )}
    >
      <form className="studio-form-stack" onSubmit={handleSubmit}>
        <ErrorText error={error} onDismiss={clearError} />
        <div className="form-group">
          <label className="field-label" htmlFor="studio-new-title">
            {t("studio.modal.projectTitle")}
          </label>
          <input
            id="studio-new-title"
            className="input-control"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t("studio.modal.projectTitlePlaceholder")}
            autoFocus
          />
        </div>

        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="studio-new-genre">
              {t("studio.modal.genre")}
            </label>
            <select
              id="studio-new-genre"
              className="input-control"
              value={effectiveGenre}
              onChange={(e) => setGenre(e.target.value)}
            >
              {genreOptions.map((g) => {
                const key = `studio.modal.genre.${g}`;
                return (
                  <option key={g} value={g}>
                    {isMessageKey(key) ? t(key) : g}
                  </option>
                );
              })}
            </select>
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="studio-new-mode">
              {t("studio.modal.mode")}
            </label>
            <select
              id="studio-new-mode"
              className="input-control"
              value={effectiveProductionMode}
              onChange={(e) => setProductionMode(e.target.value)}
            >
              {modeOptions.map((m) => {
                const key = `studio.modal.mode.${m}`;
                return (
                  <option key={m} value={m}>
                    {isMessageKey(key) ? t(key) : m}
                  </option>
                );
              })}
            </select>
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="studio-new-aspect">
              {t("studio.modal.aspectRatio")}
            </label>
            <select
              id="studio-new-aspect"
              className="input-control"
              value={effectiveAspectRatio}
              onChange={(e) => setAspectRatio(e.target.value)}
            >
              {aspectOptions.map((ar) => (
                <option key={ar} value={ar}>
                  {ar}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="form-group">
          <span className="field-label">{t("studio.modal.defaultTtsModel")}</span>
          <MediaModelSelect
            purpose="tts"
            value={defaultTtsModel}
            onChange={setDefaultTtsModel}
            disabled={saving}
          />
        </div>

        <div className="form-group">
          <label className="field-label" htmlFor="studio-new-style">
            {t("studio.modal.stylePrompt")}
          </label>
          <textarea
            id="studio-new-style"
            className="studio-textarea studio-textarea--compact"
            value={stylePrompt}
            onChange={(e) => setStylePrompt(e.target.value)}
            placeholder={t("studio.modal.stylePromptPlaceholder")}
          />
        </div>

        <div className="studio-switch-row">
          <span className="field-label">{t("studio.modal.autoApprove")}</span>
          <Switch
            checked={autoApproveBatches}
            onCheckedChange={setAutoApproveBatches}
            aria-label={t("studio.modal.autoApprove")}
          />
        </div>
      </form>
    </Dialog>
  );
};

export const DeleteProjectDialog: React.FC<{
  project: StudioProject;
  onClose: () => void;
}> = ({ project, onClose }) => {
  const t = useT();
  const deleteProject = useStudioStore((s) => s.deleteProject);
  const saving = useStudioStore((s) => s.saving);
  const error = useStudioStore((s) => s.error);
  const clearError = useStudioStore((s) => s.clearError);
  const [deleteFiles, setDeleteFiles] = useState(false);

  const handleConfirm = async () => {
    const ok = await deleteProject(project.id, deleteFiles);
    if (ok) onClose();
  };

  return (
    <Dialog
      title={t("studio.sidebar.deleteProject")}
      onClose={onClose}
      busy={saving}
      size="sm"
      footer={(requestClose) => (
        <>
          <Button variant="ghost" onClick={requestClose} disabled={saving}>
            {t("studio.modal.cancel")}
          </Button>
          <Button variant="primary" onClick={handleConfirm} disabled={saving}>
            {saving ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
            {t("studio.common.delete")}
          </Button>
        </>
      )}
    >
      <div className="studio-form-stack">
        <ErrorText error={error} onDismiss={clearError} />
        <p>{t("studio.sidebar.deleteConfirm", { title: project.title })}</p>
        <label className="studio-switch-row">
          <span className="field-label">{t("studio.sidebar.deleteWithFiles")}</span>
          <Switch
            checked={deleteFiles}
            onCheckedChange={setDeleteFiles}
            aria-label={t("studio.sidebar.deleteWithFiles")}
          />
        </label>
      </div>
    </Dialog>
  );
};
