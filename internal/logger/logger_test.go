package logger

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationKeepsBoundedBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LogFileName)
	w, err := NewRotatingWriter(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := bytes.Repeat([]byte("x"), 60)
	for i := 0; i < 10; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{path, path + ".1", path + ".2"} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("backup beyond limit must not exist: %v", err)
	}
}

func TestRotationWithoutBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), LogFileName)
	w, err := NewRotatingWriter(path, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("no backups expected")
	}
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New(Options{Level: "loud", Format: "text", Stdout: true}); err == nil {
		t.Error("bad level accepted")
	}
	if _, err := New(Options{Level: "info", Format: "xml", Stdout: true}); err == nil {
		t.Error("bad format accepted")
	}
	if _, err := New(Options{Level: "info", Format: "text"}); err == nil {
		t.Error("logger without sinks accepted")
	}
}

func TestSecretsAreRedacted(t *testing.T) {
	dir := t.TempDir()
	l, err := New(Options{Level: "debug", Format: "json", Dir: dir, MaxSizeBytes: 1 << 20, MaxBackups: 1})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("login", "auth_token", "hunter2", slog.String("Authorization", "Bearer x"), "user", "bob")
	_ = l.Close()

	data, err := os.ReadFile(filepath.Join(dir, LogFileName))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "hunter2") || strings.Contains(s, "Bearer x") {
		t.Fatalf("secret leaked: %s", s)
	}
	if !strings.Contains(s, "bob") {
		t.Fatalf("non-secret missing: %s", s)
	}
}

func TestCrashDump(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteCrashDump(dir, "test", "boom", []byte("stack-trace"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(path), CrashFilePrefix) || !strings.Contains(string(data), "boom") || !strings.Contains(string(data), "stack-trace") {
		t.Fatalf("unexpected dump %s:\n%s", path, data)
	}
}
