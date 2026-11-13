import React, { useMemo, useState } from "react";
import {
  Check,
  FolderOpen,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  RotateCcw,
  Sparkles,
  Trash2,
  Users,
  X,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import {
  Actions,
  type AgentProviderID,
  type SkillInfo,
  type SubagentInfo,
} from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { rtClient } from "../../lib/rtClient";
import { toolLabel } from "./toolLabels";
import { useAgentStore } from "./useAgentStore";

type FilterKind = "all" | "skills" | "subagents";

type EditorState =
  | null
  | {
      kind: "skill";
      isNew: boolean;
      name: string;
      description: string;
      userInvocable: boolean;
      enabled: boolean;
      body: string;
    }
  | {
      kind: "subagent";
      isNew: boolean;
      name: string;
      description: string;
      skills: string[];
      tools: string[];
      provider: string;
      model: string;
      enabled: boolean;
      body: string;
    };

export interface AgentCustomizationsTabProps {
  coreConnected: boolean;
  canEdit: boolean;
}

export const AgentCustomizationsTab: React.FC<AgentCustomizationsTabProps> = ({
  coreConnected,
  canEdit,
}) => {
  const t = useT();
  const skills = useAgentStore((s) => s.skills);
  const subagents = useAgentStore((s) => s.subagents);
  const availableTools = useAgentStore((s) => s.availableSubagentTools);
  const loading = useAgentStore((s) => s.loadingCustomizations);
  const providers = useAgentStore((s) => s.providers);

  const reloadCustomizations = useAgentStore((s) => s.reloadCustomizations);
  const saveSkill = useAgentStore((s) => s.saveSkill);
  const deleteSkill = useAgentStore((s) => s.deleteSkill);
  const restoreSkill = useAgentStore((s) => s.restoreSkill);
  const saveSubagent = useAgentStore((s) => s.saveSubagent);
  const deleteSubagent = useAgentStore((s) => s.deleteSubagent);
  const restoreSubagent = useAgentStore((s) => s.restoreSubagent);

  const [filter, setFilter] = useState<FilterKind>("all");
  const [editor, setEditor] = useState<EditorState>(null);
  const [saving, setSaving] = useState(false);

  const activeSkills = useMemo(() => skills.filter((s) => !s.tombstoned && s.enabled), [skills]);

  const startNewSkill = () => {
    setEditor({
      kind: "skill",
      isNew: true,
      name: "",
      description: "",
      userInvocable: true,
      enabled: true,
      body: "## Goal\n\n## Steps\n- ",
    });
  };

  const startEditSkill = (sk: SkillInfo) => {
    setEditor({
      kind: "skill",
      isNew: false,
      name: sk.name,
      description: sk.description,
      userInvocable: sk.user_invocable,
      enabled: sk.enabled,
      body: sk.body,
    });
  };

  const startNewSubagent = () => {
    setEditor({
      kind: "subagent",
      isNew: true,
      name: "",
      description: "",
      skills: [],
      tools: [],
      provider: "",
      model: "",
      enabled: true,
      body: "You are a focused specialist inside DezuxkStudio.\n\n- Inspect inputs before drafting findings.\n- Return concise, factual results.",
    });
  };

  const startEditSubagent = (sa: SubagentInfo) => {
    setEditor({
      kind: "subagent",
      isNew: false,
      name: sa.name,
      description: sa.description,
      skills: [...(sa.skills ?? [])],
      tools: [...(sa.tools ?? [])],
      provider: sa.provider ?? "",
      model: sa.model ?? "",
      enabled: sa.enabled,
      body: sa.body,
    });
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editor || saving) return;
    setSaving(true);
    try {
      let ok = false;
      if (editor.kind === "skill") {
        ok = await saveSkill({
          name: editor.name.trim().toLowerCase(),
          description: editor.description.trim(),
          user_invocable: editor.userInvocable,
          enabled: editor.enabled,
          body: editor.body,
        });
      } else {
        ok = await saveSubagent({
          name: editor.name.trim().toLowerCase(),
          description: editor.description.trim(),
          skills: editor.skills,
          tools: editor.tools,
          provider: (editor.provider || undefined) as AgentProviderID | undefined,
          model: editor.model.trim() || undefined,
          enabled: editor.enabled,
          body: editor.body,
        });
      }
      if (ok) {
        setEditor(null);
      }
    } finally {
      setSaving(false);
    }
  };

  const toggleSkillEnabled = async (sk: SkillInfo) => {
    await saveSkill({
      name: sk.name,
      description: sk.description,
      user_invocable: sk.user_invocable,
      enabled: !sk.enabled,
      body: sk.body,
    });
  };

  const toggleSubagentEnabled = async (sa: SubagentInfo) => {
    await saveSubagent({
      name: sa.name,
      description: sa.description,
      skills: sa.skills ?? [],
      tools: sa.tools ?? [],
      provider: sa.provider,
      model: sa.model,
      enabled: !sa.enabled,
      body: sa.body,
    });
  };

  const toggleEditorList = (field: "skills" | "tools", value: string) => {
    if (!editor || editor.kind !== "subagent") return;
    const cur = editor[field];
    const next = cur.includes(value) ? cur.filter((x) => x !== value) : [...cur, value];
    setEditor({ ...editor, [field]: next });
  };

  const openCustomizationsFolder = () => {
    void rtClient.request(Actions.SYSTEM_OPEN_FOLDER, { target: "agent_customizations" });
  };

  const stateBadgeLabel = (state: SkillInfo["source_state"]) => {
    switch (state) {
      case "managed":
        return t("agent.cust.state.managed");
      case "user_modified":
        return t("agent.cust.state.user_modified");
      case "user_only":
        return t("agent.cust.state.user_only");
      case "tombstone":
        return t("agent.cust.state.tombstone");
    }
  };

  const showSkills = filter === "all" || filter === "skills";
  const showSubagents = filter === "all" || filter === "subagents";
  const visibleCount = (showSkills ? skills.length : 0) + (showSubagents ? subagents.length : 0);

  return (
    <div className="agent-cust-tab scrollable-pane">
      <div className="agent-cust-tab__header">
        <p className="agent-cust-tab__subtitle">{t("agent.cust.subtitle")}</p>
        <div className="agent-cust-tab__tools">
          <Button
            size="sm"
            onClick={() => void reloadCustomizations()}
            disabled={!coreConnected || loading}
            title={t("agent.cust.reload")}
          >
            <RefreshCw size={12} className={loading ? "animate-spin" : ""} />
            {t("agent.cust.reload")}
          </Button>
          {canEdit && (
            <Button
              size="sm"
              onClick={openCustomizationsFolder}
              disabled={!coreConnected}
              title={t("agent.cust.openFolder")}
            >
              <FolderOpen size={12} />
              {t("agent.cust.openFolder")}
            </Button>
          )}
        </div>
      </div>

      {!canEdit && <p className="agent-cust-locked">{t("agent.cust.lockedHint")}</p>}

      <div className="agent-cust-bar">
        <div className="agent-cust-filters" role="group">
          <button
            type="button"
            className={`agent-cust-filter${filter === "all" ? " is-active" : ""}`}
            onClick={() => setFilter("all")}
          >
            {t("agent.cust.filterAll")}
          </button>
          <button
            type="button"
            className={`agent-cust-filter${filter === "skills" ? " is-active" : ""}`}
            onClick={() => setFilter("skills")}
          >
            {t("agent.cust.filterSkills", { count: skills.length })}
          </button>
          <button
            type="button"
            className={`agent-cust-filter${filter === "subagents" ? " is-active" : ""}`}
            onClick={() => setFilter("subagents")}
          >
            {t("agent.cust.filterSubagents", { count: subagents.length })}
          </button>
        </div>

        {canEdit && (
          <div className="agent-cust-create">
            <Button size="sm" onClick={startNewSkill} disabled={!coreConnected}>
              <Plus size={12} />
              {t("agent.cust.newSkill")}
            </Button>
            <Button size="sm" onClick={startNewSubagent} disabled={!coreConnected}>
              <Plus size={12} />
              {t("agent.cust.newSubagent")}
            </Button>
          </div>
        )}
      </div>

      {editor && (
        <form className="agent-cust-editor" onSubmit={(e) => void handleSave(e)}>
          <div className="agent-cust-editor__head">
            <h3 className="agent-cust-editor__title">
              {editor.kind === "skill"
                ? editor.isNew
                  ? t("agent.cust.form.skillTitleNew")
                  : t("agent.cust.form.skillTitleEdit", { name: editor.name })
                : editor.isNew
                  ? t("agent.cust.form.subagentTitleNew")
                  : t("agent.cust.form.subagentTitleEdit", { name: editor.name })}
            </h3>
            <Button size="sm" iconOnly type="button" onClick={() => setEditor(null)}>
              <X size={12} />
            </Button>
          </div>

          <label className="agent-cust-field">
            <span>{t("agent.cust.form.name")}</span>
            <input
              type="text"
              required
              disabled={!editor.isNew}
              pattern="[a-z0-9][a-z0-9_-]*"
              placeholder={t("agent.cust.form.namePlaceholder")}
              value={editor.name}
              onChange={(e) => setEditor({ ...editor, name: e.target.value })}
            />
          </label>

          <label className="agent-cust-field">
            <span>{t("agent.cust.form.description")}</span>
            <input
              type="text"
              required
              placeholder={t("agent.cust.form.descriptionPlaceholder")}
              value={editor.description}
              onChange={(e) => setEditor({ ...editor, description: e.target.value })}
            />
          </label>

          <div className="agent-cust-checks">
            <label className="agent-cust-check">
              <input
                type="checkbox"
                checked={editor.enabled}
                onChange={(e) => setEditor({ ...editor, enabled: e.target.checked })}
              />
              <span>{t("agent.cust.form.enabled")}</span>
            </label>
            {editor.kind === "skill" && (
              <label className="agent-cust-check">
                <input
                  type="checkbox"
                  checked={editor.userInvocable}
                  onChange={(e) => setEditor({ ...editor, userInvocable: e.target.checked })}
                />
                <span>{t("agent.cust.form.userInvocable")}</span>
              </label>
            )}
          </div>

          {editor.kind === "subagent" && (
            <>
              <div className="agent-cust-row">
                <label className="agent-cust-field">
                  <span>{t("agent.cust.form.providerOverride")}</span>
                  <select
                    value={editor.provider}
                    onChange={(e) => setEditor({ ...editor, provider: e.target.value })}
                  >
                    <option value="">{t("agent.cust.form.providerAuto")}</option>
                    {providers
                      .filter((p) => p.supported)
                      .map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name}
                        </option>
                      ))}
                  </select>
                </label>

                <label className="agent-cust-field">
                  <span>{t("agent.cust.form.modelOverride")}</span>
                  <input
                    type="text"
                    placeholder={t("agent.cust.form.modelPlaceholder")}
                    value={editor.model}
                    onChange={(e) => setEditor({ ...editor, model: e.target.value })}
                  />
                </label>
              </div>

              {activeSkills.length > 0 && (
                <div className="agent-cust-field">
                  <span>{t("agent.cust.form.boundSkills")}</span>
                  <div className="agent-cust-pillgrid">
                    {activeSkills.map((sk) => {
                      const picked = editor.skills.includes(sk.name);
                      return (
                        <button
                          key={sk.name}
                          type="button"
                          className={`agent-cust-pick${picked ? " is-picked" : ""}`}
                          onClick={() => toggleEditorList("skills", sk.name)}
                        >
                          {picked && <Check size={10} />}
                          <span>{sk.name}</span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              <div className="agent-cust-field">
                <span>{t("agent.cust.form.allowedTools")}</span>
                <div className="agent-cust-pillgrid">
                  {availableTools.map((toolName) => {
                    const picked = editor.tools.includes(toolName);
                    return (
                      <button
                        key={toolName}
                        type="button"
                        className={`agent-cust-pick${picked ? " is-picked" : ""}`}
                        onClick={() => toggleEditorList("tools", toolName)}
                        title={toolName}
                      >
                        {picked && <Check size={10} />}
                        <span>{toolLabel(t, toolName)}</span>
                      </button>
                    );
                  })}
                </div>
              </div>
            </>
          )}

          <label className="agent-cust-field">
            <span>{t("agent.cust.form.body")}</span>
            <textarea
              rows={6}
              value={editor.body}
              onChange={(e) => setEditor({ ...editor, body: e.target.value })}
            />
          </label>

          <div className="agent-cust-editor__actions">
            <Button size="sm" type="button" onClick={() => setEditor(null)}>
              {t("agent.cust.form.cancel")}
            </Button>
            <Button variant="primary" size="sm" type="submit" disabled={saving || !coreConnected}>
              <Check size={12} />
              {t("agent.cust.form.save")}
            </Button>
          </div>
        </form>
      )}

      {visibleCount === 0 ? (
        <p className="agent-cust-empty">{t("agent.cust.empty")}</p>
      ) : (
        <div className="agent-cust-list">
          {showSkills &&
            skills.map((sk) => (
              <div
                key={`skill-${sk.name}`}
                className={`agent-cust-card${!sk.enabled || sk.tombstoned ? " is-muted" : ""}`}
              >
                <div className="agent-cust-card__top">
                  <div className="agent-cust-card__title">
                    <Sparkles size={13} />
                    <strong>{sk.name}</strong>
                    {sk.user_invocable && !sk.tombstoned && (
                      <code className="agent-cust-slash">/{sk.name}</code>
                    )}
                    <Badge>{stateBadgeLabel(sk.source_state)}</Badge>
                    {!sk.enabled && !sk.tombstoned && <Badge>{t("agent.cust.disabled")}</Badge>}
                  </div>
                  {canEdit && (
                    <div className="agent-cust-card__actions">
                      {sk.tombstoned ? (
                        <Button
                          size="sm"
                          onClick={() => void restoreSkill(sk.name)}
                          disabled={!coreConnected}
                        >
                          <RotateCcw size={11} />
                          {t("agent.cust.restore")}
                        </Button>
                      ) : (
                        <>
                          <Button
                            size="sm"
                            iconOnly
                            onClick={() => void toggleSkillEnabled(sk)}
                            disabled={!coreConnected}
                            title={sk.enabled ? t("agent.cust.disable") : t("agent.cust.enable")}
                            aria-label={
                              sk.enabled ? t("agent.cust.disable") : t("agent.cust.enable")
                            }
                          >
                            <Power size={12} />
                          </Button>
                          <Button
                            size="sm"
                            iconOnly
                            onClick={() => startEditSkill(sk)}
                            disabled={!coreConnected}
                            title={t("agent.cust.edit")}
                            aria-label={t("agent.cust.edit")}
                          >
                            <Pencil size={12} />
                          </Button>
                          {sk.built_in && sk.source_state === "user_modified" && (
                            <Button
                              size="sm"
                              iconOnly
                              onClick={() => void restoreSkill(sk.name)}
                              disabled={!coreConnected}
                              title={t("agent.cust.restore")}
                              aria-label={t("agent.cust.restore")}
                            >
                              <RotateCcw size={12} />
                            </Button>
                          )}
                          <Button
                            size="sm"
                            iconOnly
                            onClick={() => void deleteSkill(sk.name)}
                            disabled={!coreConnected}
                            title={t("agent.cust.delete")}
                            aria-label={t("agent.cust.delete")}
                          >
                            <Trash2 size={12} />
                          </Button>
                        </>
                      )}
                    </div>
                  )}
                </div>
                <p className="agent-cust-card__desc">{sk.description}</p>
              </div>
            ))}

          {showSubagents &&
            subagents.map((sa) => (
              <div
                key={`subagent-${sa.name}`}
                className={`agent-cust-card${!sa.enabled || sa.tombstoned ? " is-muted" : ""}`}
              >
                <div className="agent-cust-card__top">
                  <div className="agent-cust-card__title">
                    <Users size={13} />
                    <strong>{sa.name}</strong>
                    <Badge>{stateBadgeLabel(sa.source_state)}</Badge>
                    {!sa.enabled && !sa.tombstoned && <Badge>{t("agent.cust.disabled")}</Badge>}
                    {sa.provider && <Badge>{sa.provider}</Badge>}
                  </div>
                  {canEdit && (
                    <div className="agent-cust-card__actions">
                      {sa.tombstoned ? (
                        <Button
                          size="sm"
                          onClick={() => void restoreSubagent(sa.name)}
                          disabled={!coreConnected}
                        >
                          <RotateCcw size={11} />
                          {t("agent.cust.restore")}
                        </Button>
                      ) : (
                        <>
                          <Button
                            size="sm"
                            iconOnly
                            onClick={() => void toggleSubagentEnabled(sa)}
                            disabled={!coreConnected}
                            title={sa.enabled ? t("agent.cust.disable") : t("agent.cust.enable")}
                            aria-label={
                              sa.enabled ? t("agent.cust.disable") : t("agent.cust.enable")
                            }
                          >
                            <Power size={12} />
                          </Button>
                          <Button
                            size="sm"
                            iconOnly
                            onClick={() => startEditSubagent(sa)}
                            disabled={!coreConnected}
                            title={t("agent.cust.edit")}
                            aria-label={t("agent.cust.edit")}
                          >
                            <Pencil size={12} />
                          </Button>
                          {sa.built_in && sa.source_state === "user_modified" && (
                            <Button
                              size="sm"
                              iconOnly
                              onClick={() => void restoreSubagent(sa.name)}
                              disabled={!coreConnected}
                              title={t("agent.cust.restore")}
                              aria-label={t("agent.cust.restore")}
                            >
                              <RotateCcw size={12} />
                            </Button>
                          )}
                          <Button
                            size="sm"
                            iconOnly
                            onClick={() => void deleteSubagent(sa.name)}
                            disabled={!coreConnected}
                            title={t("agent.cust.delete")}
                            aria-label={t("agent.cust.delete")}
                          >
                            <Trash2 size={12} />
                          </Button>
                        </>
                      )}
                    </div>
                  )}
                </div>
                <p className="agent-cust-card__desc">{sa.description}</p>
                {!sa.tombstoned && (
                  <div className="agent-cust-card__meta">
                    {(sa.skills?.length ?? 0) > 0 && (
                      <span>skills: {(sa.skills ?? []).join(", ")}</span>
                    )}
                    <span>
                      {(sa.tools?.length ?? 0) > 0
                        ? `tools: ${(sa.tools ?? []).length}`
                        : "tools: read-only"}
                    </span>
                  </div>
                )}
              </div>
            ))}
        </div>
      )}
    </div>
  );
};
