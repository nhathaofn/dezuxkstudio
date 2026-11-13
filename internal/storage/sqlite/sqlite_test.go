package sqlite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"app/internal/domain/agent"
	"app/internal/domain/preferences"
)

func open(t *testing.T, dir string) *DB {
	t.Helper()
	db, err := Open(context.Background(), Options{
		Path:          filepath.Join(dir, "test.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	var n int
	if err := db.sql.QueryRow("SELECT COUNT(*) FROM _schema_migrations").Scan(&n); err != nil || n == 0 {
		t.Fatalf("migrations not recorded: %d %v", n, err)
	}
	_ = db.Close()

	db = open(t, dir)
	defer db.Close()
	var again int
	_ = db.sql.QueryRow("SELECT COUNT(*) FROM _schema_migrations").Scan(&again)
	if again != n {
		t.Fatalf("re-open re-applied migrations: %d → %d", n, again)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	if _, err := db.sql.Exec("INSERT INTO _schema_migrations (version, name) VALUES (999, 'future.sql')"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err := Open(context.Background(), Options{
		Path: filepath.Join(dir, "test.db"), BackupDir: dir, BackupsToKeep: 1,
		BusyTimeout: time.Second, Logger: slog.New(slog.DiscardHandler),
	})
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v", err)
	}
}

func TestEmbeddedMigrationsAreWellFormed(t *testing.T) {
	ms, err := loadMigrations(migrationFS)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range ms {
		if i > 0 && m.version <= ms[i-1].version {
			t.Fatalf("migrations not strictly ordered: %v", ms)
		}
	}
}

func TestBackupPrunesOldSnapshots(t *testing.T) {
	dir := t.TempDir()
	db := open(t, dir)
	defer db.Close()
	backups := filepath.Join(dir, "backups")
	for i := 0; i < 4; i++ {
		if err := db.backup(context.Background(), backups, i+1, 2); err != nil {
			t.Fatal(err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(backups, "test-v*.db"))
	if len(matches) != 2 {
		t.Fatalf("want 2 backups, got %v", matches)
	}
}

func TestPreferencesRepository(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewPreferencesRepository(db)
	ctx := context.Background()

	if _, found, err := repo.Load(ctx); err != nil || found {
		t.Fatalf("empty db: found=%v err=%v", found, err)
	}
	want := preferences.Preferences{SchemaVersion: preferences.SchemaVersion, Theme: preferences.ThemeDark, Language: preferences.LanguageVietnamese}
	if err := repo.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := repo.Load(ctx)
	if err != nil || !found || got != want {
		t.Fatalf("round trip: %+v %v %v", got, found, err)
	}

	if _, err := db.sql.Exec("UPDATE user_settings SET value = '{broken' WHERE key = ?", preferencesKey); err != nil {
		t.Fatal(err)
	}
	got, found, err = repo.Load(ctx)
	if err != nil || !found || got.SchemaVersion != 0 {
		t.Fatalf("corrupt doc should load as legacy: %+v %v %v", got, found, err)
	}
}

func TestBinariesRepository(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewBinariesRepository(db)
	ctx := context.Background()

	// Initially empty
	p, err := repo.GetCustomPath(ctx, "ffmpeg")
	if err != nil || p != "" {
		t.Fatalf("expected empty custom path, got %q, err: %v", p, err)
	}

	all, err := repo.GetAllCustomPaths(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("expected empty map, got %v, err: %v", all, err)
	}

	// Set custom path
	if err := repo.SetCustomPath(ctx, "ffmpeg", "C:\\tools\\ffmpeg.exe"); err != nil {
		t.Fatal(err)
	}
	p, err = repo.GetCustomPath(ctx, "ffmpeg")
	if err != nil || p != "C:\\tools\\ffmpeg.exe" {
		t.Fatalf("expected C:\\tools\\ffmpeg.exe, got %q, err: %v", p, err)
	}

	// Update custom path
	if err := repo.SetCustomPath(ctx, "ffmpeg", "C:\\new\\ffmpeg.exe"); err != nil {
		t.Fatal(err)
	}
	p, err = repo.GetCustomPath(ctx, "ffmpeg")
	if err != nil || p != "C:\\new\\ffmpeg.exe" {
		t.Fatalf("expected C:\\new\\ffmpeg.exe, got %q, err: %v", p, err)
	}
}

func TestAgentRepository(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewAgentRepository(db)
	ctx := context.Background()

	p, err := repo.GetCustomPath(ctx, agent.ProviderAntigravity)
	if err != nil || p != "" {
		t.Fatalf("expected empty custom path, got %q, err: %v", p, err)
	}

	if err := repo.SetCustomPath(ctx, agent.ProviderAntigravity, "C:\\tools\\agy.exe"); err != nil {
		t.Fatal(err)
	}
	p, err = repo.GetCustomPath(ctx, agent.ProviderAntigravity)
	if err != nil || p != "C:\\tools\\agy.exe" {
		t.Fatalf("expected C:\\tools\\agy.exe, got %q, err: %v", p, err)
	}

	all, err := repo.GetAllCustomPaths(ctx)
	if err != nil || all[agent.ProviderAntigravity] != "C:\\tools\\agy.exe" {
		t.Fatalf("unexpected custom path map: %+v, err=%v", all, err)
	}

	if err := repo.SetCustomPath(ctx, agent.ProviderAntigravity, ""); err != nil {
		t.Fatal(err)
	}
	p, err = repo.GetCustomPath(ctx, agent.ProviderAntigravity)
	if err != nil || p != "" {
		t.Fatalf("expected cleared custom path, got %q, err: %v", p, err)
	}

	if err := repo.AppendChatMessage(ctx, agent.ChatMessage{
		ID:         "m1",
		ProviderID: agent.ProviderCodex,
		Role:       "user",
		Text:       "hello",
		CreatedAt:  1000,
	}); err != nil {
		t.Fatal(err)
	}
	msgs, err := repo.ListChatMessages(ctx, 50)
	if err != nil || len(msgs) != 1 || msgs[0].Text != "hello" {
		t.Fatalf("unexpected chat messages: %+v, err=%v", msgs, err)
	}
	if err := repo.ClearChatMessages(ctx); err != nil {
		t.Fatal(err)
	}
	msgs, err = repo.ListChatMessages(ctx, 50)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("expected cleared chat messages, got %+v, err=%v", msgs, err)
	}

	if err := repo.AddAllowedDir(ctx, "D:\\media\\project1"); err != nil {
		t.Fatal(err)
	}
	dirs, err := repo.ListAllowedDirs(ctx)
	if err != nil || len(dirs) != 1 || dirs[0] != "D:\\media\\project1" {
		t.Fatalf("unexpected allowed dirs: %+v, err=%v", dirs, err)
	}
	if err := repo.RemoveAllowedDir(ctx, "D:\\media\\project1"); err != nil {
		t.Fatal(err)
	}
	dirs, err = repo.ListAllowedDirs(ctx)
	if err != nil || len(dirs) != 0 {
		t.Fatalf("expected empty allowed dirs, got %+v, err=%v", dirs, err)
	}
}

func TestAgentConversations(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewAgentRepository(db)
	ctx := context.Background()

	for _, c := range []agent.Conversation{
		{ID: "a", Title: "first chat", ProviderID: agent.ProviderCodex, CreatedAt: 1, UpdatedAt: 1},
		{ID: "b", Title: "second chat", ProviderID: agent.ProviderClaude, CreatedAt: 2, UpdatedAt: 2},
	} {
		if err := repo.SaveConversation(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	_ = repo.AppendChatMessage(ctx, agent.ChatMessage{ID: "a1", Role: "user", Text: "hi", CreatedAt: 1, ConversationID: "a"})
	_ = repo.AppendChatMessage(ctx, agent.ChatMessage{ID: "b1", Role: "user", Text: "yo", CreatedAt: 2, ConversationID: "b"})
	// Later activity in "a" moves it to the top; its title is kept.
	if err := repo.SaveConversation(ctx, agent.Conversation{ID: "a", Title: "other", ProviderID: agent.ProviderCodex, CreatedAt: 9, UpdatedAt: 9}); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListConversations(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != "a" || list[0].Title != "first chat" || list[0].MessageCount != 1 || list[0].CreatedAt != 1 {
		t.Fatalf("unexpected conversations: %+v err=%v", list, err)
	}
	msgs, err := repo.ListConversationMessages(ctx, "b", 10)
	if err != nil || len(msgs) != 1 || msgs[0].ID != "b1" || msgs[0].ConversationID != "b" {
		t.Fatalf("messages must be scoped to their conversation: %+v err=%v", msgs, err)
	}
	if err := repo.DeleteConversation(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if left, _ := repo.ListConversations(ctx, 10); len(left) != 1 || left[0].ID != "b" {
		t.Fatalf("delete must remove the conversation: %+v", left)
	}
	if gone, _ := repo.ListConversationMessages(ctx, "a", 10); len(gone) != 0 {
		t.Fatalf("delete must remove its messages: %+v", gone)
	}
}

func TestAgentListConversationMessagesUnboundedWhenLimitNonPositive(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	repo := NewAgentRepository(db)
	if err := repo.SaveConversation(ctx, agent.Conversation{
		ID: "long", Title: "long chat", ProviderID: agent.ProviderCodex, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	const total = 235
	for i := range total {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := repo.AppendChatMessage(ctx, agent.ChatMessage{
			ID: fmt.Sprintf("m-%03d", i), Role: role, Text: fmt.Sprintf("msg-%03d", i),
			CreatedAt: int64(i + 1), ConversationID: "long", ProviderID: agent.ProviderCodex,
		}); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := repo.ListConversationMessages(ctx, "long", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != total || msgs[0].ID != "m-000" || msgs[total-1].ID != "m-234" {
		t.Fatalf("expected all %d messages in chronological order, got len=%d", total, len(msgs))
	}
	capped, err := repo.ListConversationMessages(ctx, "long", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 50 || capped[0].ID != "m-185" || capped[49].ID != "m-234" {
		t.Fatalf("expected last 50 chronological messages when limit=50, got len=%d first=%q last=%q", len(capped), capped[0].ID, capped[len(capped)-1].ID)
	}
}

func TestAgentRememberedConnection(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	r := NewAgentRepository(db)
	if id, err := r.RememberedConnection(ctx); err != nil || id != "" {
		t.Fatalf("empty: %q %v", id, err)
	}
	if err := r.SetRememberedConnection(ctx, "claude"); err != nil {
		t.Fatal(err)
	}
	if id, _ := r.RememberedConnection(ctx); id != "claude" {
		t.Fatalf("got %q", id)
	}
	if err := r.SetRememberedConnection(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if id, _ := r.RememberedConnection(ctx); id != "" {
		t.Fatalf("forget failed: %q", id)
	}
}

func TestWALConcurrentReadsWithWriter(t *testing.T) {
	ctx := context.Background()
	db := open(t, t.TempDir())
	defer db.Close()
	if db.reader == db.sql {
		t.Fatal("expected distinct reader pool for file-backed WAL database")
	}
	// Reader pool enforces query_only(1) so writes cannot accidentally use reader handle.
	if _, err := db.read().ExecContext(ctx, "DELETE FROM user_settings"); err == nil {
		t.Fatal("expected reader pool to reject writes via PRAGMA query_only")
	}

	r := NewAgentRepository(db)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := range 6 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 25 {
				if _, err := r.ListConversations(ctx, 20); err != nil {
					errs <- fmt.Errorf("reader %d step %d: %w", id, i, err)
					return
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 25 {
			if err := r.SetRememberedConnection(ctx, agent.ProviderID(fmt.Sprintf("p-%d", i))); err != nil {
				errs <- fmt.Errorf("writer step %d: %w", i, err)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
