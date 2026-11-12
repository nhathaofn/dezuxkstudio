package provider

// Outcome classifies a connectivity test (mirrors ArcReel's classify_probe_failure).
type Outcome string

const (
	OutcomeOK          Outcome = "ok"
	OutcomeAuthFailed  Outcome = "auth_failed"
	OutcomeRateLimited Outcome = "rate_limited"
	OutcomeNotFound    Outcome = "not_found"
	OutcomeHTTPError   Outcome = "http_error"
	OutcomeTimeout     Outcome = "timeout"
	OutcomeNetwork     Outcome = "network"
	OutcomeInvalid     Outcome = "invalid_credentials"
)

// Outcomes lists every outcome (exported to TS).
func Outcomes() []Outcome {
	return []Outcome{OutcomeOK, OutcomeAuthFailed, OutcomeRateLimited, OutcomeNotFound, OutcomeHTTPError,
		OutcomeTimeout, OutcomeNetwork, OutcomeInvalid}
}
