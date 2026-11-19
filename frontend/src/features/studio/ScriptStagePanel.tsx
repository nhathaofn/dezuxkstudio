import React, { useRef, useState } from "react";
import { ArrowRight, FileText, Loader2, Save, Sparkles, Upload } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { NodeTypes, type StudioEpisodeDetailResult } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { delegateStudioStageToAgent, isTextProviderConfigured } from "../agent/screenContext";
import { useProvidersStore } from "../providers/useProvidersStore";
import { useNodeSpec } from "../workflow/useNodeSpecs";
import { useStudioStore } from "./useStudioStore";

export const ScriptStagePanel: React.FC<{
  detail: StudioEpisodeDetailResult;
  modelOverride: string;
}> = ({ detail, modelOverride }) => {
  const t = useT();
  const scriptSpec = useNodeSpec(NodeTypes.SCRIPT_NORMALIZE);
  const saveScript = useStudioStore((s) => s.saveScript);
  const runStage = useStudioStore((s) => s.runStage);
  const setActiveStage = useStudioStore((s) => s.setActiveStage);
  const saving = useStudioStore((s) => s.saving);
  const runningStage = useStudioStore((s) => s.runningStage);
  const providerDefaults = useProvidersStore((s) => s.defaults);
  const hasTextProvider = isTextProviderConfigured(providerDefaults, modelOverride);

  const serverRaw = detail.raw_script ?? "";
  const [localDraft, setLocalDraft] = useState<string | null>(null);
  const draft = localDraft ?? serverRaw;
  const fileInputRef = useRef<HTMLInputElement>(null);

  const busy = saving || runningStage !== null;
  const parsed = detail.normalized_script;
  const scenes = parsed?.scenes ?? [];
  const beatTotal = scenes.reduce((acc, sc) => acc + (sc.beats ?? []).length, 0);

  const handleFileImport = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    const text = await file.text();
    setLocalDraft(text);
    const ok = await saveScript(text);
    if (ok) setLocalDraft(null);
  };

  const handleSaveScript = async () => {
    const ok = await saveScript(draft);
    if (ok) setLocalDraft(null);
  };

  const handleNormalize = async () => {
    if (draft !== serverRaw) {
      const ok = await saveScript(draft);
      if (!ok) return;
      setLocalDraft(null);
    }
    if (!hasTextProvider) {
      delegateStudioStageToAgent({
        projectId: detail.project.id,
        episodeId: detail.episode.id,
        stage: "script_normalize",
        prompt: t("studio.agentDelegate.scriptNormalize", {
          episodeId: detail.episode.id,
          projectId: detail.project.id,
        }),
      });
      return;
    }
    await runStage("script_normalize", modelOverride ? { model: modelOverride } : {});
  };

  return (
    <div className="studio-script-grid">
      <Card
        icon={FileText}
        title={t("studio.script.rawTitle")}
        actions={
          <div className="studio-toolbar">
            <input
              ref={fileInputRef}
              type="file"
              accept=".txt,.md,.markdown"
              className="studio-hidden-input"
              onChange={handleFileImport}
            />
            <Button
              variant="secondary"
              size="sm"
              onClick={() => fileInputRef.current?.click()}
              disabled={busy}
            >
              <Upload size={13} />
              {t("studio.script.importFile")}
            </Button>
          </div>
        }
      >
        <textarea
          className="studio-textarea"
          value={draft}
          onChange={(e) => setLocalDraft(e.target.value)}
          placeholder={t("studio.script.placeholder")}
          aria-label={t("studio.script.rawTitle")}
          disabled={busy}
        />

        <div className="studio-toolbar">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void handleSaveScript()}
            disabled={busy || draft === serverRaw}
          >
            <Save size={13} />
            {saving ? t("studio.common.saving") : t("studio.script.saveScript")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => void handleNormalize()}
            disabled={busy || draft.trim().length === 0}
            title={!hasTextProvider ? t("studio.agentDelegate.tooltip") : scriptSpec?.description}
          >
            {runningStage === "script_normalize" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Sparkles size={13} />
            )}
            {t("studio.script.normalize")}
          </Button>
        </div>
      </Card>

      <Card
        icon={Sparkles}
        title={t("studio.script.parsedTitle")}
        actions={
          <div className="studio-toolbar">
            {parsed && (
              <Badge>
                {t("studio.script.sceneCount", { scenes: scenes.length, beats: beatTotal })}
              </Badge>
            )}
            {parsed && (
              <Button variant="primary" size="sm" onClick={() => setActiveStage("assets")}>
                {t("studio.script.nextToAssets")}
                <ArrowRight size={13} />
              </Button>
            )}
          </div>
        }
      >
        {!parsed ? (
          <div className="studio-media-empty">{t("studio.script.parsedEmpty")}</div>
        ) : (
          <div className="studio-scene-list">
            {scenes.map((sc) => (
              <div key={sc.code} className="studio-scene-box">
                <div className="studio-item-head">
                  <strong>
                    {sc.code} — {sc.title}
                  </strong>
                  {sc.location && <Badge>{sc.location}</Badge>}
                </div>
                {(sc.beats ?? []).map((bt) => (
                  <div key={bt.code} className="studio-beat-row">
                    <div className="studio-item-head">
                      <span>
                        <strong>{bt.code}</strong> ·{" "}
                        {t("studio.common.secondsShort", { sec: bt.duration_sec })}
                      </span>
                      {bt.asset_tags && bt.asset_tags.length > 0 && (
                        <span>{bt.asset_tags.join(" ")}</span>
                      )}
                    </div>
                    <span>{bt.description}</span>
                  </div>
                ))}
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
};
