package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
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

func TestByteSliceVsByteArrayTSTypes(t *testing.T) {
	g := &generator{enums: map[reflect.Type]string{}, structs: map[string]reflect.Type{}}
	sliceTS, err := g.tsType(reflect.TypeFor[[]byte]())
	if err != nil || sliceTS != "string" {
		t.Fatalf("[]byte -> %q err=%v; want string", sliceTS, err)
	}
	arrTS, err := g.tsType(reflect.TypeFor[[4]byte]())
	if err != nil || arrTS != "number[]" {
		t.Fatalf("[4]byte -> %q err=%v; want number[]", arrTS, err)
	}
}
