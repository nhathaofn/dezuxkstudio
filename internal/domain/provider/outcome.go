package provider

import "time"

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
	// OutcomeSecretMissing: metadata exists but the secret store lost the key (e.g. memory backend after restart).
	OutcomeSecretMissing Outcome = "secret_missing"
)

// Outcomes lists every outcome (exported to TS).
func Outcomes() []Outcome {
	return []Outcome{OutcomeOK, OutcomeAuthFailed, OutcomeRateLimited, OutcomeNotFound, OutcomeHTTPError,
		OutcomeTimeout, OutcomeNetwork, OutcomeInvalid, OutcomeSecretMissing}
}

// Dead reports outcomes that mean the key itself is unusable (shown red); network-type failures are not.
func (o Outcome) Dead() bool {
	return o == OutcomeAuthFailed || o == OutcomeInvalid || o == OutcomeSecretMissing
}

// Health is the result of the latest connectivity test. A zero TestedAt means never tested.
type Health struct {
	Outcome  Outcome
	Detail   string
	TestedAt time.Time
}
