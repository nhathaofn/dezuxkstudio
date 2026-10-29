package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/buildinfo"
	"app/internal/fsutil"
)

// CrashFilePrefix names crash dump files so diagnostics can collect them.
const CrashFilePrefix = "crash_"

// WriteCrashDump writes a crash report for an unrecovered or recovered panic into dir
// and returns its path.
func WriteCrashDump(dir, source string, value any, stack []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("logger: create crash dir: %w", err)
	}
	now := time.Now().UTC()
	path := filepath.Join(dir, fmt.Sprintf("%s%s.log", CrashFilePrefix, now.Format("20060102T150405.000000000Z")))

	info := buildinfo.Get()
	var b strings.Builder
	fmt.Fprintf(&b, "=== CRASH REPORT ===\n")
	fmt.Fprintf(&b, "Time:    %s\n", now.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "Source:  %s\n", source)
	fmt.Fprintf(&b, "Product: %s %s (%s, built %s)\n", info.ProductName, info.Version, info.Commit, info.BuildDate)
	fmt.Fprintf(&b, "Runtime: %s %s/%s\n", info.GoVersion, info.OS, info.Arch)
	fmt.Fprintf(&b, "Panic:   %v\n\n=== STACK ===\n%s\n", value, stack)

	if err := fsutil.WriteFileAtomic(path, []byte(b.String()), filePerm); err != nil {
		return "", err
	}
	return path, nil
}
