package sessionstorage

import (
	cloudspanner "cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/postgres"
	"github.com/impulseframework/impulse/session/sessionstorage/internal/spanner"
	"github.com/jackc/pgx/v5"
)

var (
	_ PreauthStore = (*Preauth[pgx.Tx])(nil)
	_ PreauthStore = (*Preauth[*cloudspanner.ReadWriteTransaction])(nil)
)

// Preauth is the session storage implementation for Preauth. Tx is the database's
// transaction type.
type Preauth[Tx any] struct {
	sessionStorage
	// custom attaches the custom session data configuration to the driver.
	custom customDataAttacher[Tx]
}

// NewSpannerPreauth is the function that you use to create the session manager that handles the session creation and updates.
// Attach custom session data with the store's WithCustomSessionData.
func NewSpannerPreauth(db *cloudspanner.Client, opts ...SpannerOption) *Preauth[*cloudspanner.ReadWriteTransaction] {
	driver := spanner.NewSessionStorageDriver(db)
	for _, opt := range opts {
		opt.applySpanner(driver)
	}

	store := &Preauth[*cloudspanner.ReadWriteTransaction]{
		sessionStorage: newSessionStorage(driver),
		custom:         spannerCustomData{driver},
	}
	store.own(store)

	return store
}

// NewPostgresPreauth is the function that you use to create the session manager that handles the session creation and updates.
// Attach custom session data with the store's WithCustomSessionData.
func NewPostgresPreauth(db postgres.Queryer, opts ...PostgresOption) *Preauth[pgx.Tx] {
	driver := postgres.NewSessionStorageDriver(db)
	for _, opt := range opts {
		opt.applyPostgres(driver)
	}

	store := &Preauth[pgx.Tx]{
		sessionStorage: newSessionStorage(driver),
		custom:         postgresCustomData{driver},
	}
	store.own(store)

	return store
}

// WithCustomSessionData attaches cfg, the store's one custom session data
// configuration, and returns the store. cfg is typed to the store's transaction, so a
// configuration built for the other driver does not compile here. A row a session is
// started with is written with the session; when it is started with none and cfg has
// a resolver, the resolver runs inside the session-insert transaction, and its error
// aborts the session creation. Rows are scanned into T on every authenticated request.
// A later call replaces the configuration.
func (p *Preauth[Tx]) WithCustomSessionData(cfg SessionDataConfig[Tx]) *Preauth[Tx] {
	p.custom.attachSessionData(cfg.sessionData())

	return p
}
