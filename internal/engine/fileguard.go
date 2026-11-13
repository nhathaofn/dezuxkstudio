package engine

import "context"

// FileGuard decides whether a node may read (write=false) or create/overwrite (write=true) a local path.
// It returns a non-nil error to deny. Callers that run nodes on behalf of an untrusted client (e.g. a
// remote WebSocket client of a headless server) attach one with WithFileGuard; local callers attach none.
type FileGuard func(path string, write bool) error

type fileGuardKey struct{}

// WithFileGuard returns ctx carrying guard. A nil guard leaves ctx unchanged.
func WithFileGuard(ctx context.Context, guard FileGuard) context.Context {
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, fileGuardKey{}, guard)
}

// CheckFile asks the guard attached to ctx (if any) whether path may be accessed.
// Every node that touches local files must call it before reading or writing.
func CheckFile(ctx context.Context, path string, write bool) error {
	if g, ok := ctx.Value(fileGuardKey{}).(FileGuard); ok {
		return g(path, write)
	}
	return nil
}
