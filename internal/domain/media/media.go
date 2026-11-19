package media

import (
	"context"
	"time"
)

// AssetRecord mirrors a row in media_assets.
type AssetRecord struct {
	ID             string
	Kind           string
	SourceNode     string
	FilePath       string
	RegisteredRoot string
	MIMEType       string
	SizeBytes      int64
	Width          int
	Height         int
	DurationSec    float64
	MetadataJSON   string
	CreatedAt      int64
	LastVerifiedAt int64
}

// ListFilter filters rows returned by ListAssets.
type ListFilter struct {
	Kind   string
	Limit  int
	Offset int
	Search string
}

// Repository defines the persistence contract required by media.Service.
type Repository interface {
	RegisterAsset(ctx context.Context, asset AssetRecord) (*AssetRecord, error)
	GetAsset(ctx context.Context, id string) (*AssetRecord, error)
	GetAssetByPath(ctx context.Context, filePath string) (*AssetRecord, error)
	ListAssets(ctx context.Context, filter ListFilter) ([]AssetRecord, int, error)
	DeleteAsset(ctx context.Context, id string) error
	PruneMissingAssets(ctx context.Context, missingIDs []string) (int64, error)
	PruneExpiredAssets(ctx context.Context, olderThan time.Time, maxItems int) (int64, error)
	UpdateLastVerified(ctx context.Context, id string, verifiedAt int64) error
}
