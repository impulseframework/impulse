package sessionstorage

import (
	"github.com/impulseframework/impulse/session/sessionstorage/internal/postgres"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/spanner"
)

// SpannerOption configures Spanner-backed storage at construction.
type SpannerOption interface {
	applySpanner(driver *spanner.SessionStorageDriver)
}

// PostgresOption configures PostgreSQL-backed storage at construction.
type PostgresOption interface {
	applyPostgres(driver *postgres.SessionStorageDriver)
}

// Option configures storage of either backend at construction.
type Option interface {
	SpannerOption
	PostgresOption
}
