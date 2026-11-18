import type { MessageKey, Translate } from "../../lib/i18n";

/**
 * Human-readable names for DezuxkStudio's MCP tools. Unknown tools (new backend tools, provider
 * built-ins) fall back to a prettified version of their raw name, so nothing breaks when tools are added.
 */
export const toolKeys: Record<string, MessageKey> = {
  studio_list_capabilities: "agent.tool.studio_list_capabilities",
  studio_project_list: "agent.tool.studio_project_list",
  studio_project_get: "agent.tool.studio_project_get",
  studio_project_create: "agent.tool.studio_project_create",
  studio_project_update: "agent.tool.studio_project_update",
  studio_project_delete: "agent.tool.studio_project_delete",
  studio_episode_get: "agent.tool.studio_episode_get",
  studio_episode_create: "agent.tool.studio_episode_create",
  studio_save_script: "agent.tool.studio_save_script",
  studio_asset_upsert: "agent.tool.studio_asset_upsert",
  studio_shot_upsert: "agent.tool.studio_shot_upsert",
  studio_shot_reorder: "agent.tool.studio_shot_reorder",
  studio_sync_workspace: "agent.tool.studio_sync_workspace",
  studio_episode_run_stage: "agent.tool.studio_episode_run_stage",
  studio_batch_preflight: "agent.tool.studio_batch_preflight",
  studio_queue_enqueue: "agent.tool.studio_queue_enqueue",
  studio_queue_list: "agent.tool.studio_queue_list",
  studio_queue_control: "agent.tool.studio_queue_control",
  studio_cost_summary: "agent.tool.studio_cost_summary",
  studio_pricing_upsert: "agent.tool.studio_pricing_upsert",
  studio_versions_list: "agent.tool.studio_versions_list",
  studio_version_select: "agent.tool.studio_version_select",
  studio_qc_review: "agent.tool.studio_qc_review",
  studio_grid_split: "agent.tool.studio_grid_split",
  studio_mask_edit: "agent.tool.studio_mask_edit",
  studio_extract_last_frame: "agent.tool.studio_extract_last_frame",
  studio_timeline_get: "agent.tool.studio_timeline_get",
  studio_timeline_apply_ops: "agent.tool.studio_timeline_apply_ops",
  studio_timeline_undo: "agent.tool.studio_timeline_undo",
  studio_timeline_redo: "agent.tool.studio_timeline_redo",
  studio_timeline_auto_assemble: "agent.tool.studio_timeline_auto_assemble",
  studio_media_peaks_thumbs: "agent.tool.studio_media_peaks_thumbs",
  studio_timeline_render: "agent.tool.studio_timeline_render",
  studio_timeline_export_nle: "agent.tool.studio_timeline_export_nle",
  media_inspect_asset: "agent.tool.media_inspect_asset",
  workflow_list_specs: "agent.tool.workflow_list_specs",
  workflow_run_node: "agent.tool.workflow_run_node",
  workflow_run_graph: "agent.tool.workflow_run_graph",
  binaries_list: "agent.tool.binaries_list",
  binaries_probe: "agent.tool.binaries_probe",
  binaries_download: "agent.tool.binaries_download",
  preferences_get: "agent.tool.preferences_get",
  preferences_update: "agent.tool.preferences_update",
  system_info: "agent.tool.system_info",
  diagnostics_export: "agent.tool.diagnostics_export",
  workspace_list_files: "agent.tool.workspace_list_files",
  workspace_read_file: "agent.tool.workspace_read_file",
  workspace_read_document: "agent.tool.workspace_read_document",
  workspace_search_text: "agent.tool.workspace_search_text",
  workspace_write_file: "agent.tool.workspace_write_file",
  workspace_edit_file: "agent.tool.workspace_edit_file",
  workspace_move_file: "agent.tool.workspace_move_file",
  workspace_delete_file: "agent.tool.workspace_delete_file",
  skill_read: "agent.tool.skill_read",
  subagent_run: "agent.tool.subagent_run",
};

/** Strips provider prefixes such as "mcp__dezuxk__" or "dezuxk/". */
export function bareToolName(name: string): string {
  return name.replace(/^mcp__[a-z0-9]+__/i, "").replace(/^[a-z0-9]+[/.:]/i, "");
}

export function toolLabel(t: Translate, name: string): string {
  const bare = bareToolName(name);
  const key = toolKeys[bare];
  if (key) return t(key);
  const pretty = bare.replace(/[_-]+/g, " ").trim();
  return pretty ? pretty.charAt(0).toUpperCase() + pretty.slice(1) : name;
}
