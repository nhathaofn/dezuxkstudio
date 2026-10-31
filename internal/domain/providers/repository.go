package providers

import (
	"context"
	"time"
)

// Repository specifies data access operations for dynamic providers, accounts, and secrets.
type Repository interface {
	// Provider operations
	GetProvider(ctx context.Context, id string) (*Provider, error)
	ListProviders(ctx context.Context) ([]Provider, error)
	SaveProvider(ctx context.Context, p *Provider) error
	DeleteProvider(ctx context.Context, id string) error

	// Account operations
	GetAccount(ctx context.Context, id string) (*Account, error)
	ListAccounts(ctx context.Context, providerID string) ([]Account, error)
	SaveAccount(ctx context.Context, acc *Account) error
	DeleteAccount(ctx context.Context, id string) error
	UpdateAccountStatus(ctx context.Context, id string, status AccountStatus, pausedUntil *time.Time) error

	// Secrets operations (transparent DPAPI encryption/decryption)
	GetAccountSecrets(ctx context.Context, accountID string) (*AccountSecrets, error)
	SaveAccountSecrets(ctx context.Context, sec *AccountSecrets) error

	// Features & Endpoints
	ListFeatures(ctx context.Context, providerID string) ([]ProviderFeature, error)
	SaveFeature(ctx context.Context, f *ProviderFeature) error
	GetEndpoint(ctx context.Context, providerID, featureKey string) (*DynamicEndpoint, error)
	SaveEndpoint(ctx context.Context, ep *DynamicEndpoint) error

	// Cookie Partition Rules
	ListCookieRules(ctx context.Context, providerID string) ([]CookiePartitionRule, error)
	SaveCookieRule(ctx context.Context, rule *CookiePartitionRule) error
}
