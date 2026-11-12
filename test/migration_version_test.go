package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// latestMigrationVersion reads the highest NNN_ prefix of the embedded migrations, so the
// end-to-end tests do not need an edit for every new migration.
func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "internal", "storage", "sqlite", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^(\d{3,})_[a-z0-9_]+\.sql$`)
	latest := 0
	for _, e := range entries {
		if m := re.FindStringSubmatch(e.Name()); m != nil {
			if v, _ := strconv.Atoi(m[1]); v > latest {
				latest = v
			}
		}
	}
	return latest
}
