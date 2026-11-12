package realtime

// Request describes the action being executed. Long-running handlers report progress with
// progress.Report(ctx, ...) (internal/progress) and must honour ctx cancellation (client
// cancel or idle timeout).
type Request struct {
	ID       string
	Action   string
	ClientID string
}
