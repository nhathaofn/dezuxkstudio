package system

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/domain/preferences"
	"app/internal/paths"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	base := t.TempDir()
	return paths.Paths{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Logs:   filepath.Join(base, "logs"),
	}
}

func TestNewAndInfo(t *testing.T) {
	p := testPaths(t)
	for _, mode := range []config.Mode{config.ModeDesktop, config.ModeClient, config.ModeServer} {
		t.Run(string(mode), func(t *testing.T) {
			svc := New(mode, p, func(string) error { return nil })
			if svc == nil {
				t.Fatal("New returned nil")
			}
			svc.started = time.Now().Add(-3 * time.Second)

			info := svc.Info()
			if info.Mode != mode {
				t.Fatalf("Mode = %q, want %q", info.Mode, mode)
			}
			wantBuild := contracts.NewBuildInfo(buildinfo.Get())
			if info.Build != wantBuild {
				t.Fatalf("Build = %+v, want %+v", info.Build, wantBuild)
			}
			if info.Runtime.NumCPU != runtime.NumCPU() || info.Runtime.NumCPU <= 0 {
				t.Fatalf("NumCPU = %d, want %d (> 0)", info.Runtime.NumCPU, runtime.NumCPU())
			}
			if info.Runtime.NumGoroutine < 1 {
				t.Fatalf("NumGoroutine = %d, want >= 1", info.Runtime.NumGoroutine)
			}
			if info.Runtime.AllocMB <= 0 || info.Runtime.SysMB <= 0 {
				t.Fatalf("unexpected memory metrics: AllocMB=%v SysMB=%v", info.Runtime.AllocMB, info.Runtime.SysMB)
			}
			if info.Runtime.UptimeSeconds < 2 {
				t.Fatalf("UptimeSeconds = %d, want >= 2", info.Runtime.UptimeSeconds)
			}
			wantTree := paths.ResolveWorkspaceTree(p, "", "")
			wantPaths := contracts.PathsInfo{
				Config:                 p.Config,
				Data:                   p.Data,
				Logs:                   p.Logs,
				Diagnostics:            p.Diagnostics(),
				WorkspaceRoot:          wantTree.Root,
				SuggestedWorkspaceRoot: paths.SuggestedWorkspaceRoot(buildinfo.ProductName),
				MediaDir:               wantTree.MediaDir,
				WatermarkDir:           wantTree.WatermarkDir,
				AgentWorkspace:         wantTree.WorkspaceDir,
			}
			if info.Paths != wantPaths {
				t.Fatalf("Paths = %+v, want %+v", info.Paths, wantPaths)
			}
		})
	}
}

func TestOpenFolder_ValidTargets(t *testing.T) {
	p := testPaths(t)
	wantTree := paths.ResolveWorkspaceTree(p, "", "")
	cases := []struct {
		target contracts.FolderTarget
		want   string
	}{
		{contracts.FolderLogs, p.Logs},
		{contracts.FolderData, p.Data},
		{contracts.FolderConfig, p.Config},
		{contracts.FolderDiagnostics, p.Diagnostics()},
		{contracts.FolderAgentCustomizations, p.AgentCustomizations()},
		{contracts.FolderWorkspaceRoot, wantTree.Root},
	}
	if len(cases) != len(contracts.FolderTargets()) {
		t.Fatalf("test cases (%d) do not cover all FolderTargets (%d)", len(cases), len(contracts.FolderTargets()))
	}

	for _, tc := range cases {
		t.Run(string(tc.target), func(t *testing.T) {
			var opened []string
			svc := New(config.ModeDesktop, p, func(dir string) error {
				opened = append(opened, dir)
				return nil
			})
			if err := svc.OpenFolder(tc.target); err != nil {
				t.Fatalf("OpenFolder(%q) unexpected error: %v", tc.target, err)
			}
			if len(opened) != 1 || opened[0] != tc.want {
				t.Fatalf("opened = %v, want [%q]", opened, tc.want)
			}
		})
	}
}

func TestOpenFolder_InvalidTarget(t *testing.T) {
	p := testPaths(t)
	for _, target := range []contracts.FolderTarget{"", "unknown", "backups"} {
		called := false
		svc := New(config.ModeDesktop, p, func(string) error {
			called = true
			return nil
		})
		err := svc.OpenFolder(target)
		if !apperr.IsCode(err, apperr.CodeValidation) {
			t.Fatalf("OpenFolder(%q) err = %v, want %s", target, err, apperr.CodeValidation)
		}
		if called {
			t.Fatalf("OpenFolder(%q) must not call opener on invalid target", target)
		}
	}
}

func TestOpenFolder_OpenerError(t *testing.T) {
	p := testPaths(t)
	boom := errors.New("shell open failed")
	svc := New(config.ModeDesktop, p, func(string) error {
		return boom
	})
	err := svc.OpenFolder(contracts.FolderLogs)
	if !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("OpenFolder err = %v, want %s", err, apperr.CodeUnavailable)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("OpenFolder err = %v, expected wrapped %v", err, boom)
	}
}

func TestInfo_DynamicWorkspaceTree(t *testing.T) {
	p := testPaths(t)
	customRoot := filepath.Join(t.TempDir(), "custom_workspace")
	customTree := paths.ResolveWorkspaceTree(p, customRoot, "")

	// 1. Using WithWorkspaceTreeFunc functional option
	svc := New(config.ModeDesktop, p, func(string) error { return nil }, WithWorkspaceTreeFunc(func() paths.WorkspaceTree {
		return customTree
	}))

	info := svc.Info()
	if info.Paths.WorkspaceRoot != customTree.Root {
		t.Fatalf("WorkspaceRoot = %q, want %q", info.Paths.WorkspaceRoot, customTree.Root)
	}
	if info.Paths.MediaDir != customTree.MediaDir {
		t.Fatalf("MediaDir = %q, want %q", info.Paths.MediaDir, customTree.MediaDir)
	}
	if info.Paths.WatermarkDir != customTree.WatermarkDir {
		t.Fatalf("WatermarkDir = %q, want %q", info.Paths.WatermarkDir, customTree.WatermarkDir)
	}
	if info.Paths.AgentWorkspace != customTree.WorkspaceDir {
		t.Fatalf("AgentWorkspace = %q, want %q", info.Paths.AgentWorkspace, customTree.WorkspaceDir)
	}
	wantSuggested := paths.SuggestedWorkspaceRoot(buildinfo.ProductName)
	if info.Paths.SuggestedWorkspaceRoot != wantSuggested {
		t.Fatalf("SuggestedWorkspaceRoot = %q, want %q", info.Paths.SuggestedWorkspaceRoot, wantSuggested)
	}

	// 2. Dynamic runtime update via SetWorkspaceTreeFunc
	updatedRoot := filepath.Join(t.TempDir(), "updated_workspace")
	updatedTree := paths.ResolveWorkspaceTree(p, updatedRoot, "")
	svc.SetWorkspaceTreeFunc(func() paths.WorkspaceTree {
		return updatedTree
	})

	info2 := svc.Info()
	if info2.Paths.WorkspaceRoot != updatedTree.Root {
		t.Fatalf("WorkspaceRoot after SetWorkspaceTreeFunc = %q, want %q", info2.Paths.WorkspaceRoot, updatedTree.Root)
	}
	if info2.Paths.MediaDir != updatedTree.MediaDir {
		t.Fatalf("MediaDir after SetWorkspaceTreeFunc = %q, want %q", info2.Paths.MediaDir, updatedTree.MediaDir)
	}
}

func TestOpenFolder_WorkspaceRoot_Dynamic(t *testing.T) {
	p := testPaths(t)
	customRoot := filepath.Join(t.TempDir(), "custom_workspace")
	customTree := paths.ResolveWorkspaceTree(p, customRoot, "")

	var opened string
	svc := New(config.ModeDesktop, p, func(dir string) error {
		opened = dir
		return nil
	}, WithWorkspaceTreeFunc(func() paths.WorkspaceTree {
		return customTree
	}))

	if err := svc.OpenFolder(contracts.FolderWorkspaceRoot); err != nil {
		t.Fatalf("OpenFolder(FolderWorkspaceRoot) unexpected error: %v", err)
	}
	if opened != customTree.Root {
		t.Fatalf("opened = %q, want %q", opened, customTree.Root)
	}
}

func TestContracts_Preferences_And_Patch(t *testing.T) {
	// 1. PreferencesPatch validation: empty patch fails
	emptyPatch := contracts.PreferencesPatch{}
	if err := emptyPatch.Validate(); err == nil {
		t.Fatal("empty patch must fail validation")
	}

	// 2. PreferencesPatch with only new fields passes validation
	ws := filepath.Join(t.TempDir(), "workspace")
	patchWS := contracts.PreferencesPatch{WorkspaceRoot: &ws}
	if err := patchWS.Validate(); err != nil {
		t.Fatalf("patch with WorkspaceRoot failed validation: %v", err)
	}

	onboarding := true
	patchOnboarding := contracts.PreferencesPatch{OnboardingCompleted: &onboarding}
	if err := patchOnboarding.Validate(); err != nil {
		t.Fatalf("patch with OnboardingCompleted failed validation: %v", err)
	}

	skipApproval := true
	patchSkip := contracts.PreferencesPatch{AgentSkipTrustedApproval: &skipApproval}
	if err := patchSkip.Validate(); err != nil {
		t.Fatalf("patch with AgentSkipTrustedApproval failed validation: %v", err)
	}

	// 3. Invalid WorkspaceRoot: NUL bytes
	nulWS := "C:\\invalid\x00path"
	patchNul := contracts.PreferencesPatch{WorkspaceRoot: &nulWS}
	if err := patchNul.Validate(); err == nil {
		t.Fatal("patch with NUL byte in WorkspaceRoot must fail validation")
	}

	// 4. Invalid WorkspaceRoot: relative path
	relWS := "relative/path"
	patchRel := contracts.PreferencesPatch{WorkspaceRoot: &relWS}
	if err := patchRel.Validate(); err == nil {
		t.Fatal("patch with relative WorkspaceRoot must fail validation")
	}

	// 5. Valid empty WorkspaceRoot (reset to default)
	blankWS := ""
	patchBlank := contracts.PreferencesPatch{WorkspaceRoot: &blankWS}
	if err := patchBlank.Validate(); err != nil {
		t.Fatalf("patch with blank WorkspaceRoot must pass validation: %v", err)
	}

	// 6. ToDomain converts all fields
	theme := preferences.ThemeDark
	lang := preferences.LanguageVietnamese
	fullPatch := contracts.PreferencesPatch{
		Theme:                    &theme,
		Language:                 &lang,
		WorkspaceRoot:            &ws,
		OnboardingCompleted:      &onboarding,
		AgentSkipTrustedApproval: &skipApproval,
	}
	domPatch := fullPatch.ToDomain()
	if domPatch.Theme == nil || *domPatch.Theme != theme {
		t.Fatalf("Theme = %v, want %v", domPatch.Theme, theme)
	}
	if domPatch.Language == nil || *domPatch.Language != lang {
		t.Fatalf("Language = %v, want %v", domPatch.Language, lang)
	}
	if domPatch.WorkspaceRoot == nil || *domPatch.WorkspaceRoot != ws {
		t.Fatalf("WorkspaceRoot = %v, want %v", domPatch.WorkspaceRoot, ws)
	}
	if domPatch.OnboardingCompleted == nil || *domPatch.OnboardingCompleted != onboarding {
		t.Fatalf("OnboardingCompleted = %v, want %v", domPatch.OnboardingCompleted, onboarding)
	}
	if domPatch.AgentSkipTrustedApproval == nil || *domPatch.AgentSkipTrustedApproval != skipApproval {
		t.Fatalf("AgentSkipTrustedApproval = %v, want %v", domPatch.AgentSkipTrustedApproval, skipApproval)
	}

	// 7. NewPreferences converts domain model
	domPrefs := preferences.Preferences{
		SchemaVersion:            2,
		Theme:                    preferences.ThemeDark,
		Language:                 preferences.LanguageEnglish,
		WorkspaceRoot:            ws,
		OnboardingCompleted:      true,
		AgentSkipTrustedApproval: true,
	}
	dtoPrefs := contracts.NewPreferences(domPrefs)
	if dtoPrefs.Theme != domPrefs.Theme {
		t.Fatalf("dto Theme = %v, want %v", dtoPrefs.Theme, domPrefs.Theme)
	}
	if dtoPrefs.Language != domPrefs.Language {
		t.Fatalf("dto Language = %v, want %v", dtoPrefs.Language, domPrefs.Language)
	}
	if dtoPrefs.WorkspaceRoot != domPrefs.WorkspaceRoot {
		t.Fatalf("dto WorkspaceRoot = %v, want %v", dtoPrefs.WorkspaceRoot, domPrefs.WorkspaceRoot)
	}
	if dtoPrefs.OnboardingCompleted != domPrefs.OnboardingCompleted {
		t.Fatalf("dto OnboardingCompleted = %v, want %v", dtoPrefs.OnboardingCompleted, domPrefs.OnboardingCompleted)
	}
	if dtoPrefs.AgentSkipTrustedApproval != domPrefs.AgentSkipTrustedApproval {
		t.Fatalf("dto AgentSkipTrustedApproval = %v, want %v", dtoPrefs.AgentSkipTrustedApproval, domPrefs.AgentSkipTrustedApproval)
	}
}
