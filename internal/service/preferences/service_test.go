package preferences

import (
	"context"
	"errors"
	"testing"

	"app/internal/apperr"
	"app/internal/domain/preferences"
)

type memRepo struct {
	doc     *preferences.Preferences
	saves   int
	loadErr error
}

func (m *memRepo) Load(context.Context) (preferences.Preferences, bool, error) {
	if m.loadErr != nil {
		return preferences.Preferences{}, false, m.loadErr
	}
	if m.doc == nil {
		return preferences.Preferences{}, false, nil
	}
	return *m.doc, true, nil
}

func (m *memRepo) Save(_ context.Context, p preferences.Preferences) error {
	m.doc = &p
	m.saves++
	return nil
}

func ptr[T any](v T) *T { return &v }

func TestGetReturnsDefaultsWithoutSaving(t *testing.T) {
	repo := &memRepo{}
	svc, _ := New(repo, nil)
	p, err := svc.Get(context.Background())
	if err != nil || p != preferences.Defaults() || repo.saves != 0 {
		t.Fatalf("p=%+v err=%v saves=%d", p, err, repo.saves)
	}
}

func TestUpdatePersistsAndNotifiesOnlyOnChange(t *testing.T) {
	repo := &memRepo{}
	var notified []preferences.Preferences
	svc, _ := New(repo, func(p preferences.Preferences) { notified = append(notified, p) })
	ctx := context.Background()

	p, err := svc.Update(ctx, preferences.Patch{Theme: ptr(preferences.ThemeDark)})
	if err != nil || p.Theme != preferences.ThemeDark || repo.saves != 1 || len(notified) != 1 {
		t.Fatalf("p=%+v err=%v saves=%d notified=%d", p, err, repo.saves, len(notified))
	}
	if _, err := svc.Update(ctx, preferences.Patch{Theme: ptr(preferences.ThemeDark)}); err != nil {
		t.Fatal(err)
	}
	if repo.saves != 1 || len(notified) != 1 {
		t.Fatal("no-op update must not save or notify")
	}
}

func TestUpdateRejectsInvalidValues(t *testing.T) {
	svc, _ := New(&memRepo{}, nil)
	_, err := svc.Update(context.Background(), preferences.Patch{Theme: ptr(preferences.Theme("neon"))})
	if !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("err = %v", err)
	}
}

func TestLegacyDocumentIsMigratedAndSaved(t *testing.T) {
	repo := &memRepo{doc: &preferences.Preferences{Theme: preferences.ThemeLight, Language: "xx"}}
	svc, _ := New(repo, nil)
	p, err := svc.Get(context.Background())
	if err != nil || p.Theme != preferences.ThemeLight || p.Language != preferences.LanguageEnglish || p.SchemaVersion != preferences.SchemaVersion {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	if repo.saves != 1 {
		t.Fatal("migrated document must be persisted")
	}
}

func TestNewerDocumentIsRefused(t *testing.T) {
	repo := &memRepo{doc: &preferences.Preferences{SchemaVersion: preferences.SchemaVersion + 1}}
	svc, _ := New(repo, nil)
	if _, err := svc.Get(context.Background()); !apperr.IsCode(err, apperr.CodeConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestRepositoryErrorsPropagate(t *testing.T) {
	boom := errors.New("disk")
	svc, _ := New(&memRepo{loadErr: boom}, nil)
	if _, err := svc.Get(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(nil, nil); err == nil {
		t.Fatal("nil repo accepted")
	}
}

func TestWorkspaceRootValidator_NormalizationAndSuccess(t *testing.T) {
	repo := &memRepo{}
	var notified preferences.Preferences
	targetDir := t.TempDir()

	validatorCalled := false
	validator := func(raw string) (string, error) {
		validatorCalled = true
		return targetDir, nil
	}

	svc, err := New(repo, func(p preferences.Preferences) { notified = p }, WithWorkspaceRootValidator(validator))
	if err != nil {
		t.Fatal(err)
	}

	p, err := svc.Update(context.Background(), preferences.Patch{
		WorkspaceRoot: ptr("raw/path/to/be/normalized"),
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !validatorCalled {
		t.Fatal("expected validator to be called")
	}
	if p.WorkspaceRoot != targetDir {
		t.Fatalf("expected workspace root %q, got %q", targetDir, p.WorkspaceRoot)
	}
	if repo.doc.WorkspaceRoot != targetDir {
		t.Fatalf("expected stored workspace root %q, got %q", targetDir, repo.doc.WorkspaceRoot)
	}
	if notified.WorkspaceRoot != targetDir {
		t.Fatalf("expected notified workspace root %q, got %q", targetDir, notified.WorkspaceRoot)
	}
}

func TestWorkspaceRootValidator_ErrorHandling(t *testing.T) {
	repo := &memRepo{}
	notifiedCount := 0
	expectedErr := errors.New("forbidden candidate root")

	validator := func(raw string) (string, error) {
		return "", expectedErr
	}

	svc, err := New(repo, func(preferences.Preferences) { notifiedCount++ }, WithWorkspaceRootValidator(validator))
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Update(context.Background(), preferences.Patch{
		WorkspaceRoot: ptr("invalid/path"),
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
	if repo.saves != 0 {
		t.Fatalf("expected 0 saves on validation failure, got %d", repo.saves)
	}
	if notifiedCount != 0 {
		t.Fatalf("expected 0 notifications on validation failure, got %d", notifiedCount)
	}
}

func TestWorkspaceRootValidator_SetViaSetter(t *testing.T) {
	repo := &memRepo{}
	targetDir := t.TempDir()

	svc, err := New(repo, nil)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	svc.SetWorkspaceRootValidator(func(raw string) (string, error) {
		called = true
		return targetDir, nil
	})

	p, err := svc.Update(context.Background(), preferences.Patch{
		WorkspaceRoot: ptr("something"),
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !called {
		t.Fatal("expected validator set via setter to be invoked")
	}
	if p.WorkspaceRoot != targetDir {
		t.Fatalf("expected workspace root %q, got %q", targetDir, p.WorkspaceRoot)
	}
}

func TestWorkspaceRootValidator_NotInvokedWhenWorkspaceRootNil(t *testing.T) {
	repo := &memRepo{}
	called := false
	validator := func(raw string) (string, error) {
		called = true
		return "", errors.New("should not be called")
	}

	svc, err := New(repo, nil, WithWorkspaceRootValidator(validator))
	if err != nil {
		t.Fatal(err)
	}

	p, err := svc.Update(context.Background(), preferences.Patch{
		Theme: ptr(preferences.ThemeDark),
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if called {
		t.Fatal("validator should NOT be called when WorkspaceRoot is nil")
	}
	if p.Theme != preferences.ThemeDark {
		t.Fatalf("expected theme dark, got %v", p.Theme)
	}
}

func TestUpdate_SynchronousOnChangeAndV2Fields(t *testing.T) {
	repo := &memRepo{}
	var onChangeFinished bool
	var onChangeDoc preferences.Preferences

	svc, err := New(repo, func(p preferences.Preferences) {
		// Verify that at the time onChange is invoked, repo.Save has already been called
		if repo.doc == nil || *repo.doc != p {
			t.Errorf("repo.Save must complete before onChange is invoked")
		}
		onChangeDoc = p
		onChangeFinished = true
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	p, err := svc.Update(ctx, preferences.Patch{
		OnboardingCompleted:      ptr(true),
		AgentSkipTrustedApproval: ptr(true),
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify synchronous execution: onChange must have finished before Update returned
	if !onChangeFinished {
		t.Fatal("onChange must be invoked synchronously before Update returns")
	}
	if !p.OnboardingCompleted || !p.AgentSkipTrustedApproval {
		t.Fatalf("expected v2 fields to be true: %+v", p)
	}
	if onChangeDoc != p {
		t.Fatalf("onChange doc mismatch: got %+v, want %+v", onChangeDoc, p)
	}
}
