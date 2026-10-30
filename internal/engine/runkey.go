package engine

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type runKeyContext struct{}

// runNamespace isolates workflow request IDs within DezuxkStudio.
var runNamespace = uuid.NewSHA1(uuid.NameSpaceDNS, []byte("dezuxkstudio.engine"))

// WithRunKey marks ctx as one node instance of a graph run.
func WithRunKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, runKeyContext{}, key)
}

// RunKey is the node instance's run key.
func RunKey(ctx context.Context) string {
	key, _ := ctx.Value(runKeyContext{}).(string)
	return key
}

// RequestIDFor derives a stable request ID for one send of a node run.
func RequestIDFor(key string, attempt int, parts ...any) string {
	return uuid.NewSHA1(runNamespace, fmt.Appendf(nil, "%s|%d|%v", key, attempt, parts)).String()
}
