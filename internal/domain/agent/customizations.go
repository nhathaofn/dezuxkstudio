package agent

// CustomizationKind identifies a reusable customization unit shared across all providers.
type CustomizationKind string

const (
	CustomizationSkill    CustomizationKind = "skill"
	CustomizationSubagent CustomizationKind = "subagent"
)

// AllCustomizationKinds lists all valid customization kinds.
func AllCustomizationKinds() []CustomizationKind {
	return []CustomizationKind{CustomizationSkill, CustomizationSubagent}
}

// CustomizationState tracks how a file relates to embedded built-ins and user edits.
type CustomizationState string

const (
	CustomizationManaged      CustomizationState = "managed"
	CustomizationUserModified CustomizationState = "user_modified"
	CustomizationUserOnly     CustomizationState = "user_only"
	CustomizationTombstone    CustomizationState = "tombstone"
)

// AllCustomizationStates lists all valid sync states.
func AllCustomizationStates() []CustomizationState {
	return []CustomizationState{
		CustomizationManaged,
		CustomizationUserModified,
		CustomizationUserOnly,
		CustomizationTombstone,
	}
}

// SkillInfo describes a loaded domain skill shared across all agent providers.
type SkillInfo struct {
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	UserInvocable bool               `json:"user_invocable"`
	Enabled       bool               `json:"enabled"`
	Body          string             `json:"body"`
	SourceState   CustomizationState `json:"source_state"`
	BuiltIn       bool               `json:"built_in"`
	Tombstoned    bool               `json:"tombstoned,omitempty"`
	FilePath      string             `json:"file_path,omitempty"`
	Resources     []string           `json:"resources,omitempty"`
}

// SubagentInfo describes a loaded specialized subagent shared across all agent providers.
type SubagentInfo struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Tools       []string           `json:"tools"`
	Skills      []string           `json:"skills"`
	Provider    ProviderID         `json:"provider,omitempty"`
	Model       string             `json:"model,omitempty"`
	Enabled     bool               `json:"enabled"`
	Body        string             `json:"body"`
	SourceState CustomizationState `json:"source_state"`
	BuiltIn     bool               `json:"built_in"`
	Tombstoned  bool               `json:"tombstoned,omitempty"`
	FilePath    string             `json:"file_path,omitempty"`
}

// SubagentInvocation records one subagent delegation inside an assistant chat message.
type SubagentInvocation struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Provider ProviderID `json:"provider,omitempty"`
	Status   string     `json:"status"` // "running" | "done" | "error"
	Text     string     `json:"text,omitempty"`
	Tools    []string   `json:"tools,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// CustomizationsSnapshot is the full customization state surfaced to UI clients.
type CustomizationsSnapshot struct {
	Dir            string         `json:"dir"`
	Skills         []SkillInfo    `json:"skills"`
	Subagents      []SubagentInfo `json:"subagents"`
	AvailableTools []string       `json:"available_tools"`
}
