// Package lookup finds an executable by trying sources in priority order (user's custom path,
// app tools folder, PATH, well-known install locations...). Binaries and agent CLIs share it, and
// a new source is one more Candidate in the list.
package lookup

import (
	"os/exec"

	"app/internal/fsutil"
)

// Finder returns a path, or "" when its source has nothing.
type Finder func() string

// Candidate is one place to look, tagged with the caller's source value.
type Candidate[S any] struct {
	Source S
	Find   Finder
}

// First returns the first candidate that yields a path, with its source; ok is false if none does.
func First[S any](candidates ...Candidate[S]) (path string, source S, ok bool) {
	for _, c := range candidates {
		if p := c.Find(); p != "" {
			return p, c.Source, true
		}
	}
	return "", source, false
}

// File finds path if it is an existing file.
func File(path string) Finder {
	return func() string {
		if fsutil.IsFile(path) {
			return path
		}
		return ""
	}
}

// Files finds the first existing file among paths (expand is applied first when non-nil).
func Files(expand func(string) string, paths ...string) Finder {
	return func() string {
		for _, p := range paths {
			if expand != nil {
				p = expand(p)
			}
			if fsutil.IsFile(p) {
				return p
			}
		}
		return ""
	}
}

// OnPath finds the first name resolvable through the PATH environment variable.
func OnPath(names ...string) Finder {
	return func() string {
		for _, n := range names {
			if p, err := exec.LookPath(n); err == nil && p != "" {
				return p
			}
		}
		return ""
	}
}
