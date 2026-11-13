import type { MessageKey, Translate } from "../../lib/i18n";

/**
 * Human-readable names for DezuxkStudio's MCP tools. Unknown tools (new backend tools, provider
 * built-ins) fall back to a prettified version of their raw name, so nothing breaks when tools are added.
 */
const toolKeys: Record<string, MessageKey> = {
  studio_list_capabilities: "agent.tool.studio_list_capabilities",
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
