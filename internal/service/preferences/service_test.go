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
