package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The committed TypeScript contracts must match internal/contracts.
func TestGeneratedFileIsUpToDate(t *testing.T) {
	want, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "contracts", "generated.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatal("frontend/src/contracts/generated.ts is stale; run `go run ./cmd/contractgen`")
	}
}
