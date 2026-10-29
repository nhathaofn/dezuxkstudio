// Command contractgen generates frontend/src/contracts/generated.ts from internal/contracts.
//
//	go run ./cmd/contractgen          # write the file
//	go run ./cmd/contractgen -check   # fail if the file is out of date (CI)
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"app/internal/contracts"
)

func main() {
	out := flag.String("out", filepath.Join("frontend", "src", "contracts", "generated.ts"), "output file")
	check := flag.Bool("check", false, "verify the output file is up to date instead of writing it")
	flag.Parse()

	code, err := Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "contractgen:", err)
		os.Exit(1)
	}

	if *check {
		current, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "contractgen:", err)
			os.Exit(1)
		}
		if !bytes.Equal(bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n")), code) {
			fmt.Fprintf(os.Stderr, "contractgen: %s is out of date; run `go run ./cmd/contractgen`\n", *out)
			os.Exit(1)
		}
		return
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "contractgen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, code, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "contractgen:", err)
		os.Exit(1)
	}
	fmt.Printf("contractgen: wrote %s (%d actions, %d events)\n", *out, len(contracts.Actions()), len(contracts.Events()))
}
