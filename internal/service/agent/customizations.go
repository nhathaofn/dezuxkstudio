package agent

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	domainagent "app/internal/domain/agent"
	"app/internal/fsutil"
)

//go:embed profile/customizations/skills/*/* profile/customizations/subagents/*.md
var embeddedCustomizationsFS embed.FS

const (
	customizationsRootEmbed   = "profile/customizations"
	customizationManifestFile = ".manifest.json"
)

type manifestRecord struct {
	Kind      domainagent.CustomizationKind  `json:"kind"`
	Name      string                         `json:"name"`
	RelPath   string                         `json:"rel_path"`
	BaseHash  string                         `json:"base_hash,omitempty"`
	LocalHash string                         `json:"local_hash,omitempty"`
	State     domainagent.CustomizationState `json:"state"`
}

type customizationManifest struct {
	Version int                       `json:"version"`
	Entries map[string]manifestRecord `json:"entries"`
}

type builtinCustomization struct {
	Kind    domainagent.CustomizationKind
	Name    string
	RelPath string
	Content []byte
	Hash    string
}

// customizationManager owns shared skills and subagents materialized under <data>/agent-customizations/.
type customizationManager struct {
	mu       sync.Mutex
	rootDir  string
	builtins map[string]builtinCustomization // relPath (slash) -> built-in file
}

func newCustomizationManager(rootDir string) *customizationManager {
	builtins := loadBuiltinCustomizations()
	return &customizationManager{
		rootDir:  rootDir,
		builtins: builtins,
	}
}

func sha256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func normalizeCustomizationBytes(b []byte) []byte {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return []byte(s)
}

func loadBuiltinCustomizations() map[string]builtinCustomization {
	out := make(map[string]builtinCustomization)
	_ = fs.WalkDir(embeddedCustomizationsFS, customizationsRootEmbed, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, customizationsRootEmbed+"/")
		parts := strings.Split(rel, "/")
		var kind domainagent.CustomizationKind
		var name string
		if len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md" {
			kind = domainagent.CustomizationSkill
			name = parts[1]
		} else if len(parts) == 2 && parts[0] == "subagents" && strings.HasSuffix(parts[1], ".md") {
			kind = domainagent.CustomizationSubagent
			name = strings.TrimSuffix(parts[1], ".md")
		} else {
			return nil
		}
		raw, readErr := embeddedCustomizationsFS.ReadFile(p)
		if readErr != nil {
			return nil
		}
		norm := normalizeCustomizationBytes(raw)
		out[rel] = builtinCustomization{
			Kind:    kind,
			Name:    name,
			RelPath: rel,
			Content: norm,
			Hash:    sha256HexBytes(norm),
		}
		return nil
	})
	return out
}

func (m *customizationManager) manifestPath() string {
	return filepath.Join(m.rootDir, customizationManifestFile)
}

func (m *customizationManager) diskPath(relPath string) string {
	return filepath.Join(m.rootDir, filepath.FromSlash(relPath))
}

func (m *customizationManager) ensureDirsLocked() error {
	if m.rootDir == "" {
		return nil
	}
	for _, dir := range []string{
		m.rootDir,
		filepath.Join(m.rootDir, "skills"),
		filepath.Join(m.rootDir, "subagents"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("customizations: create dir %s: %w", dir, err)
		}
	}
	return nil
}

func (m *customizationManager) loadManifestLocked() customizationManifest {
	mf := customizationManifest{Version: 1, Entries: map[string]manifestRecord{}}
	if m.rootDir == "" {
		return mf
	}
	raw, err := os.ReadFile(m.manifestPath())
	if err != nil {
		return mf
	}
	if json.Unmarshal(raw, &mf) != nil || mf.Entries == nil {
		mf = customizationManifest{Version: 1, Entries: map[string]manifestRecord{}}
	}
	if mf.Version == 0 {
		mf.Version = 1
	}
	return mf
}

func (m *customizationManager) saveManifestLocked(mf customizationManifest) error {
	if m.rootDir == "" {
		return nil
	}
	b, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(m.manifestPath(), append(b, '\n'), 0o600)
}

// EnsureSynced reconciles embedded built-in skills/subagents with user files on disk via SHA-256 manifest.
// User edits (user_modified), custom files (user_only), and intentional deletions (tombstone) are preserved.
func (m *customizationManager) EnsureSynced() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureSyncedLocked()
}

func (m *customizationManager) ensureSyncedLocked() error {
	if m.rootDir == "" {
		return nil
	}
	if err := m.ensureDirsLocked(); err != nil {
		return err
	}
	mf := m.loadManifestLocked()
	dirty := false

	// 1. Reconcile every embedded built-in customization using the 15-state SHA-256 decision matrix.
	for rel, bi := range m.builtins {
		entry, tracked := mf.Entries[rel]
		abs := m.diskPath(rel)
		rawDisk, readErr := os.ReadFile(abs)
		exists := readErr == nil
		diskHash := ""
		if exists {
			diskHash = sha256HexBytes(normalizeCustomizationBytes(rawDisk))
		}

		next := entry
		switch {
		case !tracked && !exists:
			if err := m.writeRelLocked(rel, bi.Content); err != nil {
				return err
			}
			next = manifestRecord{Kind: bi.Kind, Name: bi.Name, RelPath: rel, BaseHash: bi.Hash, LocalHash: bi.Hash, State: domainagent.CustomizationManaged}
		case !tracked && exists:
			st := domainagent.CustomizationManaged
			if diskHash != bi.Hash {
				st = domainagent.CustomizationUserModified
			}
			next = manifestRecord{Kind: bi.Kind, Name: bi.Name, RelPath: rel, BaseHash: bi.Hash, LocalHash: diskHash, State: st}
		case entry.State == domainagent.CustomizationTombstone && !exists:
			// Keep user deletion respected across upgrades; refresh BaseHash for restore metadata.
			next.BaseHash = bi.Hash
			next.LocalHash = ""
		case entry.State == domainagent.CustomizationTombstone && exists:
			st := domainagent.CustomizationManaged
			if diskHash != bi.Hash {
				st = domainagent.CustomizationUserModified
			}
			next = manifestRecord{Kind: bi.Kind, Name: bi.Name, RelPath: rel, BaseHash: bi.Hash, LocalHash: diskHash, State: st}
		case !exists:
			// File was previously materialized and removed on disk -> remember tombstone so it won't resurrect.
			next = manifestRecord{Kind: bi.Kind, Name: bi.Name, RelPath: rel, BaseHash: bi.Hash, LocalHash: "", State: domainagent.CustomizationTombstone}
		case diskHash == bi.Hash:
			next = manifestRecord{Kind: bi.Kind, Name: bi.Name, RelPath: rel, BaseHash: bi.Hash, LocalHash: diskHash, State: domainagent.CustomizationManaged}
		case entry.State == domainagent.CustomizationManaged && diskHash == entry.BaseHash:
			// Unmodified managed file with newer built-in content: fast-forward upgrade.
			if err := m.writeRelLocked(rel, bi.Content); err != nil {
				return err
			}
			next = manifestRecord{Kind: bi.Kind, Name: bi.Name, RelPath: rel, BaseHash: bi.Hash, LocalHash: bi.Hash, State: domainagent.CustomizationManaged}
		default:
			// User customized a built-in file on disk or via UI: keep user's version intact.
			next = manifestRecord{Kind: bi.Kind, Name: bi.Name, RelPath: rel, BaseHash: bi.Hash, LocalHash: diskHash, State: domainagent.CustomizationUserModified}
		}

		if !tracked || next != entry {
			mf.Entries[rel] = next
			dirty = true
		}
	}

	// 2. Reconcile non-built-in entries tracked on disk.
	onDisk := m.scanDiskFilesLocked()
	for rel, meta := range onDisk {
		if _, isBuiltin := m.builtins[rel]; isBuiltin {
			continue
		}
		entry, tracked := mf.Entries[rel]
		if !tracked || entry.State != domainagent.CustomizationUserOnly || entry.LocalHash != meta.hash {
			mf.Entries[rel] = manifestRecord{
				Kind:      meta.kind,
				Name:      meta.name,
				RelPath:   rel,
				LocalHash: meta.hash,
				State:     domainagent.CustomizationUserOnly,
			}
			dirty = true
		}
	}
	for rel, entry := range mf.Entries {
		if _, isBuiltin := m.builtins[rel]; isBuiltin {
			continue
		}
		if entry.State == domainagent.CustomizationUserOnly {
			if _, ok := onDisk[rel]; !ok {
				delete(mf.Entries, rel)
				dirty = true
			}
		}
	}

	if dirty || !fileExists(m.manifestPath()) {
		return m.saveManifestLocked(mf)
	}
	return nil
}

type diskFileMeta struct {
	kind domainagent.CustomizationKind
	name string
	hash string
}

func (m *customizationManager) scanDiskFilesLocked() map[string]diskFileMeta {
	out := make(map[string]diskFileMeta)
	if m.rootDir == "" {
		return out
	}
	skillsDir := filepath.Join(m.rootDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || validateCustomizationSlug(e.Name()) != nil {
				continue
			}
			rel := "skills/" + e.Name() + "/SKILL.md"
			if b, err := os.ReadFile(m.diskPath(rel)); err == nil {
				out[rel] = diskFileMeta{
					kind: domainagent.CustomizationSkill,
					name: e.Name(),
					hash: sha256HexBytes(normalizeCustomizationBytes(b)),
				}
			}
		}
	}
	subagentsDir := filepath.Join(m.rootDir, "subagents")
	if entries, err := os.ReadDir(subagentsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			slug := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			if validateCustomizationSlug(slug) != nil {
				continue
			}
			rel := "subagents/" + slug + ".md"
			if b, err := os.ReadFile(m.diskPath(rel)); err == nil {
				out[rel] = diskFileMeta{
					kind: domainagent.CustomizationSubagent,
					name: slug,
					hash: sha256HexBytes(normalizeCustomizationBytes(b)),
				}
			}
		}
	}
	return out
}

func (m *customizationManager) writeRelLocked(rel string, content []byte) error {
	target := m.diskPath(rel)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(target, content, 0o600)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Snapshot loads all skills and subagents (including tombstoned built-ins so UI can restore them).
func (m *customizationManager) Snapshot(availableTools []string) (domainagent.CustomizationsSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureSyncedLocked(); err != nil {
		return domainagent.CustomizationsSnapshot{}, err
	}
	mf := m.loadManifestLocked()
	skills, subagents := m.loadAllLocked(mf)
	toolsCopy := append([]string(nil), availableTools...)
	return domainagent.CustomizationsSnapshot{
		Dir:            m.rootDir,
		Skills:         skills,
		Subagents:      subagents,
		AvailableTools: toolsCopy,
	}, nil
}

func (m *customizationManager) loadAllLocked(mf customizationManifest) ([]domainagent.SkillInfo, []domainagent.SubagentInfo) {
	var skills []domainagent.SkillInfo
	var subagents []domainagent.SubagentInfo

	diskFiles := m.scanDiskFilesLocked()
	seenRel := make(map[string]bool)

	for rel, meta := range diskFiles {
		seenRel[rel] = true
		raw, err := os.ReadFile(m.diskPath(rel))
		if err != nil {
			continue
		}
		parsed, parseErr := parseMarkdownFrontmatter(raw)
		if parseErr != nil {
			continue
		}
		_, isBuiltin := m.builtins[rel]
		state := domainagent.CustomizationUserOnly
		if rec, ok := mf.Entries[rel]; ok && rec.State != "" {
			state = rec.State
		} else if isBuiltin {
			state = domainagent.CustomizationManaged
		}

		switch meta.kind {
		case domainagent.CustomizationSkill:
			name := meta.name
			desc := fmString(parsed.Fields, "description")
			userInv := fmBool(parsed.Fields, "user-invocable", true)
			enabled := fmBool(parsed.Fields, "enabled", true)
			skills = append(skills, domainagent.SkillInfo{
				Name:          name,
				Description:   desc,
				UserInvocable: userInv,
				Enabled:       enabled,
				Body:          parsed.Body,
				SourceState:   state,
				BuiltIn:       isBuiltin,
				FilePath:      m.diskPath(rel),
				Resources:     m.listSkillResourcesLocked(name),
			})
		case domainagent.CustomizationSubagent:
			name := meta.name
			desc := fmString(parsed.Fields, "description")
			tools := fmStringSlice(parsed.Fields, "tools")
			boundSkills := fmStringSlice(parsed.Fields, "skills")
			prov := domainagent.ProviderID(strings.TrimSpace(fmString(parsed.Fields, "provider")))
			model := fmString(parsed.Fields, "model")
			enabled := fmBool(parsed.Fields, "enabled", true)
			subagents = append(subagents, domainagent.SubagentInfo{
				Name:        name,
				Description: desc,
				Tools:       tools,
				Skills:      boundSkills,
				Provider:    prov,
				Model:       model,
				Enabled:     enabled,
				Body:        parsed.Body,
				SourceState: state,
				BuiltIn:     isBuiltin,
				FilePath:    m.diskPath(rel),
			})
		}
	}

	// Include tombstoned built-ins so UI shows a Restore button without cluttering active catalogs.
	for rel, bi := range m.builtins {
		if seenRel[rel] {
			continue
		}
		rec, ok := mf.Entries[rel]
		if !ok || rec.State != domainagent.CustomizationTombstone {
			continue
		}
		parsed, _ := parseMarkdownFrontmatter(bi.Content)
		if bi.Kind == domainagent.CustomizationSkill {
			skills = append(skills, domainagent.SkillInfo{
				Name:          bi.Name,
				Description:   fmString(parsed.Fields, "description"),
				UserInvocable: fmBool(parsed.Fields, "user-invocable", true),
				Enabled:       false,
				Body:          parsed.Body,
				SourceState:   domainagent.CustomizationTombstone,
				BuiltIn:       true,
				Tombstoned:    true,
			})
		} else if bi.Kind == domainagent.CustomizationSubagent {
			subagents = append(subagents, domainagent.SubagentInfo{
				Name:        bi.Name,
				Description: fmString(parsed.Fields, "description"),
				Tools:       fmStringSlice(parsed.Fields, "tools"),
				Skills:      fmStringSlice(parsed.Fields, "skills"),
				Provider:    domainagent.ProviderID(strings.TrimSpace(fmString(parsed.Fields, "provider"))),
				Model:       fmString(parsed.Fields, "model"),
				Enabled:     false,
				Body:        parsed.Body,
				SourceState: domainagent.CustomizationTombstone,
				BuiltIn:     true,
				Tombstoned:  true,
			})
		}
	}

	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	sort.Slice(subagents, func(i, j int) bool { return subagents[i].Name < subagents[j].Name })
	return skills, subagents
}

func (m *customizationManager) listSkillResourcesLocked(name string) []string {
	skillDir := filepath.Join(m.rootDir, "skills", name)
	var out []string
	_ = filepath.WalkDir(skillDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != skillDir {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(skillDir, p)
		if relErr != nil {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if slash == "SKILL.md" || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		out = append(out, slash)
		return nil
	})
	sort.Strings(out)
	return out
}

// ReadSkill loads a skill's SKILL.md body (or a named companion file inside skills/<name>/).
func (m *customizationManager) ReadSkill(name, resource string) (domainagent.SkillInfo, string, error) {
	if err := validateCustomizationSlug(name); err != nil {
		return domainagent.SkillInfo{}, "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureSyncedLocked(); err != nil {
		return domainagent.SkillInfo{}, "", err
	}
	mf := m.loadManifestLocked()
	skills, _ := m.loadAllLocked(mf)
	var found *domainagent.SkillInfo
	for i := range skills {
		if skills[i].Name == name && !skills[i].Tombstoned {
			found = &skills[i]
			break
		}
	}
	if found == nil || !found.Enabled {
		return domainagent.SkillInfo{}, "", fmt.Errorf("skill %q is not available", name)
	}

	resPath := strings.TrimSpace(resource)
	if resPath == "" || resPath == "SKILL.md" {
		return *found, found.Body, nil
	}

	skillDir := filepath.Join(m.rootDir, "skills", name)
	cleanRel := filepath.Clean(filepath.FromSlash(resPath))
	if filepath.IsAbs(cleanRel) || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return domainagent.SkillInfo{}, "", fmt.Errorf("invalid skill resource path %q", resource)
	}
	candidate := filepath.Join(skillDir, cleanRel)
	realRoot, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		return domainagent.SkillInfo{}, "", err
	}
	realTarget, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return domainagent.SkillInfo{}, "", fmt.Errorf("resource %q not found in skill %q", resource, name)
	}
	if !isWithinDir(realTarget, realRoot) {
		return domainagent.SkillInfo{}, "", fmt.Errorf("resource %q escapes skill folder", resource)
	}
	b, err := os.ReadFile(realTarget)
	if err != nil {
		return domainagent.SkillInfo{}, "", err
	}
	return *found, string(b), nil
}

// ResolveSubagent returns an active subagent specification together with the markdown bodies of all bound skills.
func (m *customizationManager) ResolveSubagent(name string) (domainagent.SubagentInfo, []domainagent.SkillInfo, error) {
	if err := validateCustomizationSlug(name); err != nil {
		return domainagent.SubagentInfo{}, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureSyncedLocked(); err != nil {
		return domainagent.SubagentInfo{}, nil, err
	}
	mf := m.loadManifestLocked()
	skills, subagents := m.loadAllLocked(mf)

	var sub *domainagent.SubagentInfo
	for i := range subagents {
		if subagents[i].Name == name && !subagents[i].Tombstoned {
			sub = &subagents[i]
			break
		}
	}
	if sub == nil || !sub.Enabled {
		return domainagent.SubagentInfo{}, nil, fmt.Errorf("subagent %q is not available", name)
	}

	skillByName := make(map[string]domainagent.SkillInfo, len(skills))
	for _, sk := range skills {
		if !sk.Tombstoned && sk.Enabled {
			skillByName[sk.Name] = sk
		}
	}
	var bound []domainagent.SkillInfo
	for _, skName := range sub.Skills {
		if sk, ok := skillByName[skName]; ok {
			bound = append(bound, sk)
		}
	}
	return *sub, bound, nil
}

func (m *customizationManager) SaveSkill(name, description string, userInvocable, enabled bool, body string) error {
	if err := validateCustomizationSlug(name); err != nil {
		return err
	}
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("skill description is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureSyncedLocked(); err != nil {
		return err
	}
	rel := "skills/" + name + "/SKILL.md"
	content := normalizeCustomizationBytes([]byte(renderSkillMarkdown(name, description, userInvocable, enabled, body)))
	if err := m.writeRelLocked(rel, content); err != nil {
		return err
	}
	return m.recordWriteLocked(domainagent.CustomizationSkill, name, rel, content)
}

func (m *customizationManager) SaveSubagent(spec domainagent.SubagentInfo) error {
	if err := validateCustomizationSlug(spec.Name); err != nil {
		return err
	}
	if strings.TrimSpace(spec.Description) == "" {
		return fmt.Errorf("subagent description is required")
	}
	if spec.Provider != "" {
		if err := domainagent.ValidateProviderID(spec.Provider); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureSyncedLocked(); err != nil {
		return err
	}
	rel := "subagents/" + spec.Name + ".md"
	content := normalizeCustomizationBytes([]byte(renderSubagentMarkdown(
		spec.Name, spec.Description, spec.Skills, spec.Tools, string(spec.Provider), spec.Model, spec.Enabled, spec.Body,
	)))
	if err := m.writeRelLocked(rel, content); err != nil {
		return err
	}
	return m.recordWriteLocked(domainagent.CustomizationSubagent, spec.Name, rel, content)
}

func (m *customizationManager) recordWriteLocked(kind domainagent.CustomizationKind, name, rel string, content []byte) error {
	mf := m.loadManifestLocked()
	hash := sha256HexBytes(content)
	if bi, isBuiltin := m.builtins[rel]; isBuiltin {
		state := domainagent.CustomizationUserModified
		if hash == bi.Hash {
			state = domainagent.CustomizationManaged
		}
		mf.Entries[rel] = manifestRecord{
			Kind:      kind,
			Name:      name,
			RelPath:   rel,
			BaseHash:  bi.Hash,
			LocalHash: hash,
			State:     state,
		}
	} else {
		mf.Entries[rel] = manifestRecord{
			Kind:      kind,
			Name:      name,
			RelPath:   rel,
			LocalHash: hash,
			State:     domainagent.CustomizationUserOnly,
		}
	}
	return m.saveManifestLocked(mf)
}

func (m *customizationManager) Delete(kind domainagent.CustomizationKind, name string) error {
	if err := validateCustomizationSlug(name); err != nil {
		return err
	}
	rel, pathOnDisk := m.targetFor(kind, name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureSyncedLocked(); err != nil {
		return err
	}
	mf := m.loadManifestLocked()
	if kind == domainagent.CustomizationSkill {
		if err := os.RemoveAll(filepath.Dir(pathOnDisk)); err != nil {
			return err
		}
	} else if err := os.Remove(pathOnDisk); err != nil && !os.IsNotExist(err) {
		return err
	}
	if bi, isBuiltin := m.builtins[rel]; isBuiltin {
		mf.Entries[rel] = manifestRecord{
			Kind:     kind,
			Name:     name,
			RelPath:  rel,
			BaseHash: bi.Hash,
			State:    domainagent.CustomizationTombstone,
		}
	} else {
		delete(mf.Entries, rel)
	}
	return m.saveManifestLocked(mf)
}

func (m *customizationManager) RestoreBuiltIn(kind domainagent.CustomizationKind, name string) error {
	if err := validateCustomizationSlug(name); err != nil {
		return err
	}
	rel, _ := m.targetFor(kind, name)
	bi, ok := m.builtins[rel]
	if !ok {
		return fmt.Errorf("%s %q is not a built-in item", kind, name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureDirsLocked(); err != nil {
		return err
	}
	if err := m.writeRelLocked(rel, bi.Content); err != nil {
		return err
	}
	mf := m.loadManifestLocked()
	mf.Entries[rel] = manifestRecord{
		Kind:      kind,
		Name:      name,
		RelPath:   rel,
		BaseHash:  bi.Hash,
		LocalHash: bi.Hash,
		State:     domainagent.CustomizationManaged,
	}
	return m.saveManifestLocked(mf)
}

func (m *customizationManager) targetFor(kind domainagent.CustomizationKind, name string) (rel, diskPath string) {
	if kind == domainagent.CustomizationSkill {
		rel = "skills/" + name + "/SKILL.md"
	} else {
		rel = "subagents/" + name + ".md"
	}
	return rel, m.diskPath(rel)
}

// PromptCatalogBlocks builds deterministic Tier-1 catalog sections (<skills> and <subagents>) for the root system prompt.
func (m *customizationManager) PromptCatalogBlocks() (skillsBlock, subagentsBlock string) {
	if m == nil {
		return "", ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureSyncedLocked(); err != nil {
		return "", ""
	}
	mf := m.loadManifestLocked()
	skills, subagents := m.loadAllLocked(mf)

	var skillLines []string
	for _, sk := range skills {
		if sk.Tombstoned || !sk.Enabled {
			continue
		}
		tag := ""
		if sk.UserInvocable {
			tag = " [/" + sk.Name + "]"
		}
		skillLines = append(skillLines, fmt.Sprintf("- %s%s: %s", sk.Name, tag, oneLine(sk.Description)))
	}
	if len(skillLines) > 0 {
		skillsBlock = "<skills>\nLoad a skill's full instructions on demand with skill_read before starting matching work.\n" +
			strings.Join(skillLines, "\n") + "\n</skills>"
	}

	var subLines []string
	for _, sa := range subagents {
		if sa.Tombstoned || !sa.Enabled {
			continue
		}
		meta := ""
		if len(sa.Skills) > 0 {
			meta += " | skills: " + strings.Join(sa.Skills, ", ")
		}
		if len(sa.Tools) > 0 {
			meta += " | tools: " + strings.Join(sa.Tools, ", ")
		} else {
			meta += " | tools: read-only"
		}
		subLines = append(subLines, fmt.Sprintf("- %s: %s%s", sa.Name, oneLine(sa.Description), meta))
	}
	if len(subLines) > 0 {
		subagentsBlock = "<subagents>\nDelegate focused sub-tasks to specialized isolated workers with subagent_run (supports parallel calls).\n" +
			strings.Join(subLines, "\n") + "\n</subagents>"
	}
	return skillsBlock, subagentsBlock
}

// ExpandSlashInvocation detects an explicit `/<skill-name> ...` prompt prefix and returns an enriched prompt directive
// preserving the original user text cleanly for history.
func (m *customizationManager) ExpandSlashInvocation(userPrompt string) string {
	trimmed := strings.TrimSpace(userPrompt)
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") {
		return userPrompt
	}
	token := trimmed[1:]
	rest := ""
	if idx := strings.IndexAny(token, " \t\r\n"); idx >= 0 {
		rest = strings.TrimSpace(token[idx+1:])
		token = token[:idx]
	}
	slug := strings.ToLower(strings.TrimSpace(token))
	if validateCustomizationSlug(slug) != nil {
		return userPrompt
	}
	sk, body, err := m.ReadSkill(slug, "")
	if err != nil || !sk.UserInvocable {
		return userPrompt
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "<invoked_skill name=%q>\n%s\n</invoked_skill>\n\n", sk.Name, strings.TrimSpace(body))
	if rest != "" {
		sb.WriteString(rest)
	} else {
		fmt.Fprintf(&sb, "Apply skill %q to the current workspace/context.", sk.Name)
	}
	return sb.String()
}
